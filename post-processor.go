package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/mitchellh/goamz/aws"
	"github.com/mitchellh/goamz/s3"
	awscommon "github.com/mitchellh/packer/builder/amazon/common"
	"github.com/mitchellh/packer/common"
	"github.com/mitchellh/packer/packer"
)

type Config struct {
	Region       string `mapstructure:"region"`
	Bucket       string `mapstructure:"bucket"`
	ManifestPath string `mapstructure:"manifest"`
	BoxName      string `mapstructure:"box_name"`
	BoxDir       string `mapstructure:"box_dir"`
	Version      string `mapstructure:"version"`

	common.PackerConfig    `mapstructure:",squash"`
	awscommon.AccessConfig `mapstructure:",squash"`
	tpl                    *packer.ConfigTemplate
}

type PostProcessor struct {
	config Config
	s3     *s3.Bucket
}

func (p *PostProcessor) Configure(raws ...interface{}) error {
	_, err := common.DecodeConfig(&p.config, raws...)
	if err != nil {
		return err
	}

	p.config.tpl, err = packer.NewConfigTemplate()
	if err != nil {
		return err
	}
	p.config.tpl.UserVars = p.config.PackerUserVars

	errs := &packer.MultiError{}

	errs = packer.MultiErrorAppend(errs, p.config.AccessConfig.Prepare(p.config.tpl)...)

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
		*ptr, err = p.config.tpl.Process(*ptr, nil)
		if err != nil {
			errs = packer.MultiErrorAppend(errs, fmt.Errorf("Error processing %s: %s", key, err))
		}
	}

	// setup the s3 bucket
	auth, err := p.config.AccessConfig.Auth()
	if err != nil {
		errs = packer.MultiErrorAppend(errs, err)
	}

	// determine region
	region, valid := aws.Regions[p.config.Region]
	if valid {
		p.s3 = s3.New(auth, region).Bucket(p.config.Bucket)
	} else {
		errs = packer.MultiErrorAppend(errs, fmt.Errorf("Invalid region specified: %s", p.config.Region))
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
	if err := manifest.add(p.config.Version, &Provider{
		Name:         provider,
		Url:          p.s3.URL(boxPath),
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
		multi, err := p.s3.Multi(boxPath, "application/octet-stream", "public-read")
		if err != nil {
			return nil, false, err
		}
		parts, err := multi.PutAll(file, 5*1024*1024)
		if err != nil {
			return nil, false, err
		}
		if err := multi.Complete(parts); err != nil {
			return nil, false, err
		}
	} else {
		if err := p.s3.PutReader(boxPath, file, size, "application/octet-stream", "public-read"); err != nil {
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
		if s3Err, ok := err.(*s3.Error); ok && s3Err.Code == "NoSuchKey" {
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
	if err := p.s3.Put(p.config.ManifestPath, buf.Bytes(), "application/json", "public-read"); err != nil {
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
