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
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
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
	ACL                 string        `mapstructure:"acl"`
	AccessKey           string        `mapstructure:"access_key_id"`
	SecretKey           string        `mapstructure:"secret_key"`
	SignedExpiry        time.Duration `mapstructure:"signed_expiry"`
	common.PackerConfig `mapstructure:",squash"`

	ctx interpolate.Context
}

type PostProcessor struct {
	config  Config
	session *session.Session
	s3      *s3.S3
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

	// create a session and an S3 service
	accessKey := p.config.AccessKey
	secretKey := p.config.SecretKey

	p.session = session.New(&aws.Config{
		Region:      aws.String(p.config.Region),
		Credentials: credentials.NewStaticCredentials(accessKey, secretKey, ""),
	})

	p.s3 = s3.New(p.session)

	// check that we have permission to access the bucket
	_, err = p.s3.HeadBucket(&s3.HeadBucketInput{
		Bucket: aws.String(p.config.Bucket),
	})

	if err != nil {
		errs = packer.MultiErrorAppend(errs, fmt.Errorf("Unable to access the bucket %s, make sure your credentials are valid and have sufficient permissions", p.config.Bucket))
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

	// determine box size
	boxStat, err := os.Stat(box)
	if err != nil {
		return nil, false, err
	}
	ui.Message(fmt.Sprintf("Box to upload: %s (%d bytes)", box, boxStat.Size()))

	// determine version
	version, err := p.determineVersion(p.config.Version)
	if err != nil {
		return nil, false, err
	}
	ui.Message(fmt.Sprintf("No version defined, using %s as new version", version))

	// generate the path to store the box in S3
	boxPath := fmt.Sprintf("%s/%s/%s", p.config.BoxDir, version, path.Base(box))

	ui.Message("Generating checksum")
	checksum, err := sum256(box)
	if err != nil {
		return nil, false, err
	}
	ui.Message(fmt.Sprintf("Checksum is %s", checksum))

	// upload the box to S3
	ui.Message(fmt.Sprintf("Uploading box to S3: %s", boxPath))
	err = p.uploadBox(box, boxPath)

	if err != nil {
		return nil, false, err
	}

	// get the latest manifest so we can add to it
	ui.Message("Fetching latest manifest")
	manifest, err := p.getManifest()
	if err != nil {
		return nil, false, err
	}

	ui.Message(fmt.Sprintf("Adding %s %s box to manifest", provider, version))
	var url string
	if p.config.SignedExpiry == 0 {
		url = generateS3Url(p.config.Region, p.config.Bucket, boxPath)
	} else {
		// fetch the new object
		boxObject, _ := p.s3.GetObjectRequest(&s3.GetObjectInput{
			Bucket: aws.String(p.config.Bucket),
			Key:    aws.String(boxPath),
		})

		url, err = boxObject.Presign(p.config.SignedExpiry)

		if err != nil {
			return nil, false, err
		}
	}
	if err := manifest.add(version, &Provider{
		Name:         provider,
		Url:          url,
		ChecksumType: "sha256",
		Checksum:     checksum,
	}); err != nil {
		return nil, false, err
	}

	ui.Message(fmt.Sprintf("Uploading the manifest: %s", p.config.ManifestPath))
	if err := p.putManifest(manifest); err != nil {
		return nil, false, err
	}

	return &Artifact{generateS3Url(p.config.Region, p.config.Bucket, p.config.ManifestPath)}, true, nil
}

func (p *PostProcessor) determineVersion(configVersion string) (string, error) {
	version := configVersion

	if version == "" {
		// get the next version based on the existing manifest
		if manifest, err := p.getManifest(); err != nil {
			return "", err
		} else {
			version = manifest.getNextVersion()
		}
	}

	return version, nil
}

func (p *PostProcessor) uploadBox(box, boxPath string) error {
	// open the file for reading
	file, err := os.Open(box)
	if err != nil {
		return err
	}
	defer file.Close()

	// upload the file
	uploader := s3manager.NewUploader(p.session, func(u *s3manager.Uploader) {
		u.PartSize = 1024 * 1024 * 64
	})

	_, err = uploader.Upload(&s3manager.UploadInput{
		Body:   file,
		Bucket: aws.String(p.config.Bucket),
		Key:    aws.String(boxPath),
		ACL:    aws.String(p.config.ACL),
	})

	return err
}

func (p *PostProcessor) getManifest() (*Manifest, error) {
	result, err := p.s3.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(p.config.Bucket),
		Key:    aws.String(p.config.ManifestPath),
	})

	if err != nil {
		if awsErr, ok := err.(awserr.Error); ok {
			if awsErr.Code() == "NoSuchKey" {
				return &Manifest{Name: p.config.BoxName}, nil
			}
		}
		return nil, err
	}

	defer result.Body.Close()

	manifest := &Manifest{}
	if err := json.NewDecoder(result.Body).Decode(manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

func (p *PostProcessor) putManifest(manifest *Manifest) error {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(manifest); err != nil {
		return err
	}

	_, err := p.s3.PutObject(&s3.PutObjectInput{
		Body:        strings.NewReader(buf.String()),
		Bucket:      aws.String(p.config.Bucket),
		Key:         aws.String(p.config.ManifestPath),
		ContentType: aws.String("application/json"),
		ACL:         aws.String(p.config.ACL),
	})

	if err != nil {
		return err
	}

	return nil
}

func generateS3Url(region, bucket, key string) string {
	if region == "us-east-1" {
		return fmt.Sprintf("https://s3.amazonaws.com/%s/%s", bucket, key)
	} else {
		return fmt.Sprintf("https://s3-%s.amazonaws.com/%s/%s", region, bucket, key)
	}
}

// calculates a sha256 checksum of the file
func sum256(filePath string) (string, error) {
	// open the file for reading
	file, err := os.Open(filePath)

	if err != nil {
		return "", err
	}

	defer file.Close()

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
