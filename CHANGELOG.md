# Change log

## 1.5.0

* make plugin compatible with Packer 1.6+ (https://github.com/lmars/packer-post-processor-vagrant-s3/pull/62)
* make plugin compatible with go modules, make configuration hcl2 compatible (https://github.com/lmars/packer-post-processor-vagrant-s3/pull/59)
* allow vagrant builder without using vagrant post-processor (https://github.com/lmars/packer-post-processor-vagrant-s3/issues/56)

## 1.4.0

* add support for overriding some S3 uploader options, useful on less reliable connections

## 1.3.3

* fix missing Linux release due to typo

## 1.3.2

* minor fix to the Travis release process 

## 1.3.1

* generate releases on Travis and host them on GitHub instead

## 1.3.0

* add this change log
* add support for specifying the S3 storage class of uploaded box files
* add support for more AWS credentials providers (static, shared, environmental) 

## 1.2.0

* Log how long it took to upload a box file to S3
* Add option to generate Cloudfront URLs instead of plain S3 URLs

## 1.1.2

* Fix invalid generated S3 URLs when using the `us-east-1` region
* Use default AWS SDK credential chain when credentials have not been explicitly defined

## 1.1.1

* Don't print "No version defined" when a version was actually defined

## 1.1.0

* Use the official Amazon AWS Go SDK instead of `goamz/aws`, box uploading is now much more reliable
* Automatically generate a new minor version if none is specified
* Better examples in the README

## 1.0.1

* Delay creation of manifest.json to avoid overwriting existing ones
* README formatting fixes

## 1.0.0

First stable release
