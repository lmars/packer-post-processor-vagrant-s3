//go:generate mapstructure-to-hcl2 -type Config

package main

import (
	"bytes"
	"context"
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
	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/hashicorp/packer/common"
	"github.com/hashicorp/packer/helper/config"
	"github.com/hashicorp/packer/packer"
	"github.com/hashicorp/packer/template/interpolate"
)

type Config struct {
	Region              string        `mapstructure:"region"`
	Bucket              string        `mapstructure:"bucket"`
	CloudFront          string        `mapstructure:"cloudfront"`
	ManifestPath        string        `mapstructure:"manifest"`
	BoxName             string        `mapstructure:"box_name"`
	BoxDir              string        `mapstructure:"box_dir"`
	Version             string        `mapstructure:"version"`
	ACL                 string        `mapstructure:"acl"`
	CredentialFile      string        `mapstructure:"credentials"`
	CredentialProfile   string        `mapstructure:"profile"`
	AccessKey           string        `mapstructure:"access_key_id"`
	SecretKey           string        `mapstructure:"secret_key"`
	SessionToken        string        `mapstructure:"session_token"`
	SignedExpiry        time.Duration `mapstructure:"signed_expiry"`
	StorageClass        string        `mapstructure:"storage_class"`
	PartSize            int64         `mapstructure:"part_size"`
	Concurrency         int           `mapstructure:"concurrency"`
	common.PackerConfig `mapstructure:",squash"`

	ctx interpolate.Context
}

type PostProcessor struct {
	config  Config
	session *session.Session
	s3      *s3.S3
}

func (p *PostProcessor) ConfigSpec() hcldec.ObjectSpec { return p.config.FlatMapstructure().HCL2Spec() }

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

	var cred *credentials.Credentials = nil // nil credentials use the default aws sdk credential chain

	if p.config.AccessKey != "" && p.config.SecretKey != "" {
		// StaticProvider if both access id and secret are defined
		// Environmental variables used:
		// $AWS_SESSION_TOKEN
		cred = credentials.NewCredentials(&credentials.StaticProvider{
			Value: credentials.Value{
				AccessKeyID:     p.config.AccessKey,
				SecretAccessKey: p.config.SecretKey,
				SessionToken:    p.config.SessionToken,
			},
		})
	} else if p.config.CredentialFile != "" || p.config.CredentialProfile != "" {
		// SharedCredentialProvider if either credentials file or a profile is defined
		// Environmental variables used:
		// $AWS_SHARED_CREDENTIALS_FILE ("$HOME/.aws/credentials" if unset)
		// $AWS_PROFILE ("default" if unset)
		cred = credentials.NewCredentials(&credentials.SharedCredentialsProvider{
			Filename: p.config.CredentialFile,
			Profile:  p.config.CredentialProfile,
		})
	} else {
		// EnvProvider as fallback if none of the above matched
		// Environmental variables used:
		// $AWS_ACCESS_KEY_ID ($AWS_ACCESS_KEY if unset)
		// $AWS_SECRET_ACCESS_KEY ($AWS_SECRET_KEY if unset)
		// $AWS_SESSION_TOKEN
		cred = credentials.NewCredentials(&credentials.EnvProvider{})
	}

	p.session = session.New(&aws.Config{
		Region:      aws.String(p.config.Region),
		Credentials: cred,
	})

	p.s3 = s3.New(p.session)

	// check that we have permission to access the bucket
	_, err = p.s3.HeadBucket(&s3.HeadBucketInput{
		Bucket: aws.String(p.config.Bucket),
	})

	if err != nil {
		errs = packer.MultiErrorAppend(errs, fmt.Errorf("Unable to access the bucket %s:\n%s\nMake sure your credentials are valid and have sufficient permissions", p.config.Bucket, err))
	}

	if p.config.ACL == "" {
		p.config.ACL = "public-read"
	}

	// set default storage class
	if p.config.StorageClass == "" {
		p.config.StorageClass = "STANDARD"
	}

	if p.config.PartSize == 0 {
		p.config.PartSize = s3manager.DefaultUploadPartSize
	}

	if p.config.Concurrency == 0 {
		p.config.Concurrency = s3manager.DefaultUploadConcurrency
	}

	if len(errs.Errors) > 0 {
		return errs
	}

	return nil
}

func (p *PostProcessor) PostProcess(context context.Context, ui packer.Ui, artifact packer.Artifact) (packer.Artifact, bool, bool, error) {

	// Only accept input from the vagrant post-processor

	if artifact.BuilderId() != "mitchellh.post-processor.vagrant" && artifact.BuilderId() != "vagrant" {
		return nil, false, false, fmt.Errorf("Unknown artifact type, requires box from vagrant post-processor: %s", artifact.BuilderId())
	}

	// Assume there is only one .box file to upload
	box := artifact.Files()[0]
	if !strings.HasSuffix(box, ".box") {
		return nil, false, false, fmt.Errorf("Unknown files in artifact from vagrant post-processor: %s", artifact.Files())
	}

	provider := providerFromBuilderName(artifact.Id())
	ui.Say(fmt.Sprintf("Preparing to upload box for '%s' provider to S3 bucket '%s'", provider, p.config.Bucket))

	// determine box size
	boxStat, err := os.Stat(box)
	if err != nil {
		return nil, false, false, err
	}
	ui.Message(fmt.Sprintf("Box to upload: %s (%d bytes)", box, boxStat.Size()))

	// determine version
	version := p.config.Version

	if version == "" {
		version, err = p.determineVersion()
		if err != nil {
			return nil, false, false, err
		}

		ui.Message(fmt.Sprintf("No version defined, using %s as new version", version))
	} else {
		ui.Message(fmt.Sprintf("Using %s as new version", version))
	}

	// generate the path to store the box in S3
	boxPath := fmt.Sprintf("%s/%s/%s", p.config.BoxDir, version, path.Base(box))

	ui.Message("Generating checksum")
	checksum, err := sum256(box)
	if err != nil {
		return nil, false, false, err
	}
	ui.Message(fmt.Sprintf("Checksum is %s", checksum))

	// upload the box to S3
	ui.Message(fmt.Sprintf("Uploading box to S3: %s, PartSize: %d, Concurrency: %d", boxPath, p.config.PartSize, p.config.Concurrency))

	start := time.Now()
	err = p.uploadBox(box, boxPath)

	if err != nil {
		return nil, false, false, err
	} else {
		elapsed := time.Since(start)
		ui.Message(fmt.Sprintf("Box upload took: %s", elapsed))
	}

	// get the latest manifest so we can add to it
	ui.Message("Fetching latest manifest")
	manifest, err := p.getManifest()
	if err != nil {
		return nil, false, false, err
	}

	ui.Message(fmt.Sprintf("Adding %s %s box to manifest", provider, version))
	var url string
	if p.config.SignedExpiry == 0 {
		url = generateS3Url(p.config.Region, p.config.Bucket, p.config.CloudFront, boxPath)
	} else {
		// fetch the new object
		boxObject, _ := p.s3.GetObjectRequest(&s3.GetObjectInput{
			Bucket: aws.String(p.config.Bucket),
			Key:    aws.String(boxPath),
		})

		url, err = boxObject.Presign(p.config.SignedExpiry)

		if err != nil {
			return nil, false, false, err
		}
	}
	if err := manifest.add(version, &Provider{
		Name:         provider,
		Url:          url,
		ChecksumType: "sha256",
		Checksum:     checksum,
	}); err != nil {
		return nil, false, false, err
	}

	ui.Message(fmt.Sprintf("Uploading the manifest: %s", p.config.ManifestPath))
	if err := p.putManifest(manifest); err != nil {
		return nil, false, false, err
	}

	return &Artifact{generateS3Url(p.config.Region, p.config.Bucket, p.config.CloudFront, p.config.ManifestPath)}, true, false, nil
}

func (p *PostProcessor) determineVersion() (string, error) {
	// get the next version based on the existing manifest
	if manifest, err := p.getManifest(); err != nil {
		return "", err
	} else {
		return manifest.getNextVersion(), nil
	}
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
		u.PartSize = p.config.PartSize
		u.Concurrency = p.config.Concurrency
	})

	_, err = uploader.Upload(&s3manager.UploadInput{
		Body:         file,
		Bucket:       aws.String(p.config.Bucket),
		Key:          aws.String(boxPath),
		ACL:          aws.String(p.config.ACL),
		StorageClass: aws.String(p.config.StorageClass),
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

func generateS3Url(region, bucket, cloudFront, key string) string {
	if cloudFront != "" {
		return fmt.Sprintf("https://%s/%s", cloudFront, key)
	}

	if region == "us-east-1" {
		return fmt.Sprintf("https://s3.amazonaws.com/%s/%s", bucket, key)
	}

	return fmt.Sprintf("https://s3-%s.amazonaws.com/%s/%s", region, bucket, key)
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
