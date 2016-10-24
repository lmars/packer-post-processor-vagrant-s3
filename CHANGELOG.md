# Change log

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
