package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"strings"
	"time"

	"github.com/mitchellh/goamz/aws"
	"github.com/mitchellh/goamz/s3"
	"github.com/mitchellh/packer/common"
	"github.com/mitchellh/packer/helper/config"
	"github.com/mitchellh/packer/packer"
	"github.com/mitchellh/packer/template/interpolate"
)

type Config struct {
	Region              string        `mapstructure:"region"`
	Bucket              string        `mapstructure:"bucket"`
	ManifestPath        string        `mapstructure:"manifest"`
	BoxName             string        `mapstructure:"box_name"`
	BoxDir              string        `mapstructure:"box_dir"`
	Version             string        `mapstructure:"version"`
	ACL                 s3.ACL        `mapstructure:"acl"`
	AccessKey           string        `mapstructure:"access_key_id"`
	SecretKey           string        `mapstructure:"secret_key"`
	SignedExpiry        time.Duration `mapstructure:"signed_expiry"`
	common.PackerConfig `mapstructure:",squash"`

	ctx interpolate.Context
}

type PostProcessor struct {
	config Config
	s3     *s3.Bucket
}

func (p *PostProcessor) Configure(raws ...interface{}) error {
	err := config.Decode(&p.config, &config.DecodeOpts{
		Interpolate:        true,
		InterpolateContext: &p.config.ctx,
		InterpolateFilter: &interpolate.RenderFilter{
			Exclude: []string{"output"},
		},
	}, raws...)
	if err != nil {
		return err
	}

	errs := new(packer.MultiError)
	// required configuration
	templates := map[string]*string{
		"region":   &p.config.Region,
		"bucket":   &p.config.Bucket,
		"manifest": &p.config.ManifestPath,
		"box_name": &p.config.BoxName,
		"box_dir":  &p.config.BoxDir,
		"version":  &p.config.Version,
	}

	for key, ptr := range templates {
		if *ptr == "" {
			errs = packer.MultiErrorAppend(errs, fmt.Errorf("vagrant-s3 %s must be set", key))
		}
	}

	// Template process
	for key, ptr := range templates {
		if err = interpolate.Validate(*ptr, &p.config.ctx); err != nil {
			errs = packer.MultiErrorAppend(
				errs, fmt.Errorf("Error parsing %s template: %s", key, err))
		}
	}

	auth, err := aws.GetAuth(p.config.AccessKey, p.config.SecretKey)
	if err != nil {
		errs = packer.MultiErrorAppend(errs, fmt.Errorf("Unable to create Aws Authentication. Try providing keys 'access_key_id' and 'secret_key'"))
	}

	// determine region
	region, valid := aws.Regions[p.config.Region]
	if valid {
		p.s3 = s3.New(auth, region).Bucket(p.config.Bucket)
	} else {
		errs = packer.MultiErrorAppend(errs, fmt.Errorf("Invalid region specified: %s", p.config.Region))
	}

	if p.config.ACL == "" {
		p.config.ACL = "public-read"
	}

	if len(errs.Errors) > 0 {
		return errs
	}

	return nil
}

func (p *PostProcessor) PostProcess(ui packer.Ui, artifact packer.Artifact) (packer.Artifact, bool, error) {
	// Only accept input from the vagrant post-processor
	if artifact.BuilderId() != "mitchellh.post-processor.vagrant" {
		return nil, false, fmt.Errorf("Unknown artifact type, requires box from vagrant post-processor: %s", artifact.BuilderId())
	}

	// Assume there is only one .box file to upload
	box := artifact.Files()[0]
	if !strings.HasSuffix(box, ".box") {
		return nil, false, fmt.Errorf("Unknown files in artifact from vagrant post-processor: %s", artifact.Files())
	}

	provider := providerFromBuilderName(artifact.Id())
	ui.Say(fmt.Sprintf("Preparing to upload box for '%s' provider to S3 bucket '%s'", provider, p.config.Bucket))

	// open the box so we can upload to S3 and calculate checksum for the manifest
	file, err := os.Open(box)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()

	// get the file's size
	info, err := file.Stat()
	if err != nil {
		return nil, false, err
	}
	size := info.Size()
	ui.Message(fmt.Sprintf("Box to upload: %s (%d bytes)", box, size))

	// get the latest manifest so we can add to it
	ui.Message("Fetching latest manifest")
	manifest, err := p.getManifest()
	if err != nil {
		return nil, false, err
	}

	// generate the path to store the box in S3
	boxPath := fmt.Sprintf("%s/%s/%s", p.config.BoxDir, p.config.Version, path.Base(box))

	ui.Message("Generating checksum")
	checksum, err := sum256(file)
	if err != nil {
		return nil, false, err
	}
	ui.Message(fmt.Sprintf("Checksum is %s", checksum))

	ui.Message(fmt.Sprintf("Adding %s %s box to manifest", provider, p.config.Version))
	var url string
	if p.config.SignedExpiry == 0 {
		url = p.s3.URL(boxPath)
	} else {
		url = p.s3.SignedURL(boxPath, time.Now().Add(p.config.SignedExpiry))
	}
	if err := manifest.add(p.config.Version, &Provider{
		Name:         provider,
		Url:          url,
		ChecksumType: "sha256",
		Checksum:     checksum,
	}); err != nil {
		return nil, false, err
	}

	// upload the box to S3 (rewinding as we already read the file to generate the checksum)
	ui.Message(fmt.Sprintf("Uploading box to S3: %s", boxPath))
	if _, err := file.Seek(0, 0); err != nil {
		return nil, false, err
	}
	if size > 100*1024*1024 {
		ui.Message("File size > 100MB. Initiating multipart upload")

		multi, err := p.s3.InitMulti(boxPath, "application/octet-stream", p.config.ACL)
		if err != nil {
			return nil, false, err
		}

		ui.Message("Uploading...")

		const chunkSize = 5 * 1024 * 1024

		totalParts := int(math.Ceil(float64(size) / float64(chunkSize)))
		totalUploadSize := int64(0)

		parts := make([]s3.Part, totalParts)

		errorCount := 0

		for partNum := int(1); partNum <= totalParts; partNum++ {

			filePos, err := file.Seek(0, 1)

			partSize := int64(math.Min(chunkSize, float64(size-filePos)))
			partBuffer := make([]byte, partSize)

			ui.Message(fmt.Sprintf("Upload: Uploading part %d of %d, %d (of max %d) bytes", partNum, int(totalParts), int(partSize), int(chunkSize)))

			readBytes, err := file.Read(partBuffer)
			ui.Message(fmt.Sprintf("Upload: Read %d bytes from box file on disk", readBytes))

			bufferReader := bytes.NewReader(partBuffer)
			part, err := multi.PutPart(partNum, bufferReader)

			parts[partNum-1] = part

			if err != nil {

				if errorCount < 10 {
					errorCount++
					ui.Message(fmt.Sprintf("Error encountered! %s. Retry %d.", err, errorCount))
					time.Sleep(5 * time.Second)
					//reset seek position to the beginning of this block
					file.Seek(filePos, 0)
					partNum--
				} else {
					ui.Message(fmt.Sprintf("Too many errors encountered! Aborting.", err, errorCount))
					return nil, false, err
				}
			} else {

				totalUploadSize += part.Size
				ui.Message(fmt.Sprintf("Upload: Finished part %d of %d, upload total is %d bytes. This part was %d bytes.", partNum, totalParts, int(totalUploadSize), int(part.Size)))
			}
		}

		ui.Message("Parts uploaded, completing upload...")
		if err := multi.Complete(parts); err != nil {
			return nil, false, err
		}
	} else {
		if err := p.s3.PutReader(boxPath, file, size, "application/octet-stream", p.config.ACL); err != nil {
			return nil, false, err
		}
	}

	ui.Message(fmt.Sprintf("Uploading the manifest: %s", p.config.ManifestPath))
	if err := p.putManifest(manifest); err != nil {
		return nil, false, err
	}

	return &Artifact{p.s3.URL(p.config.ManifestPath)}, true, nil
}

func (p *PostProcessor) getManifest() (*Manifest, error) {
	body, err := p.s3.GetReader(p.config.ManifestPath)
	if err != nil {
		s3Err, ok := err.(*s3.Error)
		if ok && s3Err.Code == "NoSuchKey" {
			return &Manifest{Name: p.config.BoxName}, nil
		}
		return nil, err
	}
	defer body.Close()

	manifest := &Manifest{}
	if err := json.NewDecoder(body).Decode(manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

func (p *PostProcessor) putManifest(manifest *Manifest) error {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(manifest); err != nil {
		return err
	}
	if err := p.s3.Put(p.config.ManifestPath, buf.Bytes(), "application/json", p.config.ACL); err != nil {
		return err
	}
	return nil
}

// calculates a sha256 checksum of the file
func sum256(file *os.File) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// converts a packer builder name to the corresponding vagrant provider
func providerFromBuilderName(name string) string {
	switch name {
	case "aws":
		return "aws"
	case "digitalocean":
		return "digitalocean"
	case "virtualbox":
		return "virtualbox"
	case "vmware":
		return "vmware_desktop"
	case "parallels":
		return "parallels"
	default:
		return name
	}
}
