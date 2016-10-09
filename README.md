Packer Vagrant S3 post-processor
================================

Uploads built Vagrant boxes to S3 and manages a manifest file for versioned boxes.

Installation
------------

### Pre-built binaries

The easiest way to install this post-processor is to download a pre-built binary. The builds are hosted 
[here](http://packer-post-processor-vagrant-s3-build-host.negge.fi/). Follow the link, download the correct binary for your 
platform, then rename the file to `packer-post-processor-vagrant-s3` and place it in `~/.packer.d/plugins` so 
that Packer can find it (create the directory if it doesn't exist).

### Building from source

You'll need git and go installed for this. First, download the code by running the following command:

```
$ go get github.com/lmars/packer-post-processor-vagrant-s3
```
Then, copy the plugin into `~/.packer.d/plugins` directory:

```
$ mkdir $HOME/.packer.d/plugins
$ cp $GOPATH/bin/packer-post-processor-vagrant-s3 $HOME/.packer.d/plugins

```
Usage
-----

Add the post-processor to your packer template **after** the `vagrant` post-processor:

```json
{
  "variables": {
    "version":  "0.0.1",
    "box_organization": "my-organization",
    "box_name": "my-cool-project"
  },
  "builders": [ ... ],
  "provisioners": [ ... ],
  "post-processors": [
    [
      {
        "type": "vagrant"
        ...
      },
      {
        "type":     "vagrant-s3",
        "region": "us-east-1",
        "bucket":   "my-s3-bucket",
        "manifest": "vagrant/json/{{ user `box_organization` }}/{{ user `box_name` }}.json",
        "box_dir":  "vagrant/boxes/{{ user `box_organization` }}/{{ user `box_name` }}",
        "box_name": "{{ user `box_organization` }}{{ user `box_name` }}",
        "version":  "{{ user `version` }}"
      }
    ]
  ]
}
```

**NOTE:** The post-processors must be a **nested array** (i.e.: a Packer sequence definition) so that they run in order. See the [Packer template documentation](http://www.packer.io/docs/templates/post-processors.html) for more information.

The above will result in the following objects being created in S3:

```
https://s3.amazonaws.com/my-s3-bucket/vagrant/json/my-organization/my-cool-project.json
https://s3.amazonaws.com/my-s3-bucket/vagrant/boxes/my-organization/my-cool-project/0.0.1/packer_virtualbox-iso_virtualbox.box
```

You can now use point your `Vagrantfile` to the manifest by using something like this:

```ruby
Vagrant.configure(2) do |config|
  config.vm.box = "my-organization/my-cool-project"
  config.vm.box_url = "https://s3.amazonaws.com/my-s3-bucket/vagrant/json/my-organization/my-cool-project.json"
end
```

When pointing the `Vagrantfile` at a manifest instead of directly at a box you retain traditional features such as
versioning and multiple providers.

Configuration
-------------

All configuration properties are **required**, except where noted.
### region

The S3 region of the bucket (please see next section).

Valid values:

* us-east-1
* us-gov-west-1
* us-west-1
* us-west-2
* eu-west-1
* eu-central-1
* ap-southeast-1
* ap-southeast-2
* ap-northeast-1
* sa-east-1
* cn-north-1

### bucket

The S3 bucket name where you want to upload the box and the manifest.

### manifest

The path to the manifest file in your bucket. If you don't have a manifest, don't worry, one will be created.  **We recommend that you name you manifest the same as your box.**

This controls what users of your box will set `vm.config.box_url` to in their `Vagrantfile` (e.g. `https://s3.amazonaws.com/my-s3-bucket/vagrant/manifest.json`).

### box_name

The name of your box.

This is what users of your box will set `vm.config.box` to in their `Vagrantfile`.

### box_dir

The path to a directory in your bucket to store boxes in (e.g. `vagrant/boxes`).

### version (optional)

The version of the box you are uploading. The box will be uploaded to a S3 directory path that includes the version number (e.g. `vagrant/boxes/<version>`).

Only one box can be uploaded per provider for a given version. If you are building an updated box, you should bump this version, meaning users of your box will be made aware of the new version.

You may also omit `version` completely, in which case the version will automatically be bumped to the next minor revision (e.g. if you have versions `0.0.1` and `0.0.2` in your manifest the new version will become `0.1.0`).

### acl (optional)

An access control setting for the uploaded files (both the manifest and box files).

Valid values:

* private
* public-read
* public-read-write (not recommended)
* authenticated-read
* bucket-owner-read
* bucket-owner-full-control

If not set, will use `public-read`.

### access_key_id (optional)

Your AWS access key.

If not set, will use the standard aws sdk credential chain - http://docs.aws.amazon.com/sdk-for-go/latest/v1/developerguide/configuring-sdk.title.html

### secret_key (optional)

Your AWS secret key.

If not set, will use the standard aws sdk credential chain - http://docs.aws.amazon.com/sdk-for-go/latest/v1/developerguide/configuring-sdk.title.html

### signed_expiry (optional)

If set, generate presigned urls in the manifest valid for a time duration like "60s" or "12h".
See https://golang.org/pkg/time/#ParseDuration for syntax.

If not set, generate normal s3 urls.

### cloudfront (optional)

If set, this will be used as the base url for all urls.
This can not be used in combination with the signed_expiry setting.

If not set, generate normal s3 urls.

