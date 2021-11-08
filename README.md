Packer Vagrant S3 post-processor
================================

[![Build Status](https://github.com/lmars/packer-plugin-vagrant-s3/actions/workflows/release.yml/badge.svg)](https://github.com/lmars/packer-plugin-vagrant-s3/actions/workflows/release.yml)

Uploads built Vagrant boxes to S3 and manages a manifest file for versioned boxes.

Installation
------------

### Pre-built binaries

#### Using the `packer init` command

Starting from version 1.7, Packer supports a new `packer init` command allowing
automatic installation of Packer plugins. Read the
[Packer documentation](https://www.packer.io/docs/commands/init) for more information.

To install this plugin, copy and paste this code into your Packer configuration .
Then, run [`packer init`](https://www.packer.io/docs/commands/init).

```hcl
packer {
  required_plugins {
    amazon = {
      version = ">= 1.6.0"
      source  = "github.com/lmars/vagrant-s3"
    }
  }
}
```


#### Manual installation

You can find pre-built binary releases of the plugin [here](https://github.com/lmars/packer-plugin-vagrant-s3/releases).
Once you have downloaded the latest archive corresponding to your target OS,
uncompress it to retrieve the plugin binary file corresponding to your platform.
To install the plugin, please follow the Packer documentation on
[installing a plugin](https://www.packer.io/docs/extending/plugins/#installing-plugins).

Usage
-----

Add the post-processor to your packer template **after** the `vagrant` post-processor:

```hcl
packer {
  required_plugins {
    vagrant-s3 = {
      version = ">= 1.6.0"
      source  = "github.com/lmars/vagrant-s3"
    }
  }
}

variable "version" {
  type    = string
  default = "0.0.1"
}

variable "box_organization" {
  type    = string
  default = "my-organization"
}

variable "box_name" {
  type    = string
  default = "my-cool-project"
}

source "virtualbox-iso" "my-cool-project" { ... }

build {
  sources = ["source.virtualbox-iso.browsertest"]

  provisioner "file" { ... }

  post-processors {
    post-processor "vagrant" { ... }
    post-processor "vagrant-s3" {
      box_dir     = "vagrant/boxes/${var.box_organisation}/${var.box_name}"
      box_name    = "${var.box_organisation}/${var.box_name}"
      bucket      = "my-s3-bucket"
      manifest    = "vagrant/json/${var.box_organisation}/${var.box_name}.json"
      region      = "us-east-1"
      version     = "${var.version}"
    }
  }
}
```

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

Providers
-------------

There are three AWS credential providers that are used:

* Static credentials
* Shared credentials
* Environmental credentials

### Static credentials configuration

Static credentials are used when **both** of the configuration variables `access_key_id` and
`secret_key` are defined:

```json
{
  "post-processors": [
    [
      {
        "type":     "vagrant-s3",
        ...
        "access_key_id": "AKIAIOSFODNN7EXAMPLE",
        "secret_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
        "session_token": "{{ user `session_token` }}",
        ...
      }
    ]
  ]
}
```

It isn't necessary to define the `session_token` variable, even when using the STS token service for
authenticating. By default the session token will be generated and cached during the initial request.

The only reason to define `session_token` is when a token has already been generated on behalf of the
build process via external means (ie: a corporate SSO end-point). You can define this using the
environmental variable `$AWS_SESSION_TOKEN` or by passing it in from the command line:

```shell
$ packer build -var "session_token=$TOKEN" template.json
```

### Shared credentials configuration

Shared credentials are used when **either** of the configuration variables are defined:

```json
{
  "post-processors": [
    [
      {
        "type":     "vagrant-s3",
        ...
        "credentials": "~/.aws/credentials",
        "profile": "vagrant-s3-creds",
      }
    ]
  ]
}
```

The above configuration will use the `[vagrant-s3-creds]` profile from the credentials file located at
`~/.aws/credentials`:

```
[vagrant-s3-creds]
aws_access_key_id = AKIAIOSFODNN7EXAMPLE
aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
```

If `credentials` is not defined then the environmental variable `$AWS_SHARED_CREDENTIALS_FILE` will
be used to look for a credentials file, otherwise it will look for it in the default location
(`$HOME/.aws/credentials`).

If the `profile` variable is not defined, the environmental variable `$AWS_PROFILE` will be used,
otherwise the `[default]` profile will be used if it exists.

### Environmental credentials configuration

Environmental credentials are used when **none** of the other variables are defined and will instead
attempt to populate the credentials using the following environmental variables:

*  `$AWS_ACCESS_KEY_ID` (`$AWS_ACCESS_KEY` if unset)
*  `$AWS_SECRET_ACCESS_KEY` (`$AWS_SECRET_KEY` if unset)
*  `$AWS_SESSION_TOKEN`

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

### signed_expiry (optional)

If set, generate presigned urls in the manifest valid for a time duration like "60s" or "12h".
See https://golang.org/pkg/time/#ParseDuration for syntax.

If not set, generate normal s3 urls.

### cloudfront (optional)

If set, this will be used as the base url for all urls.
This can not be used in combination with the signed_expiry setting.

If not set, generate normal s3 urls.

### storage_class (optional)

If set, the S3 storage class for the uploaded box file will be set to this. Defaults to `STANDARD`, which is the
default for S3. Valid values are `STANDARD`, `STANDARD_IA` (infrequent access), or `REDUCED_REDUNDANCY`.

### part_size (optional)

The part size (in bytes) to use when uploading the box to S3. See the AWS SDK for the default value.

### concurrency (optional)

The concurrency to use when uploading the box to S3. See the AWS SDK for the default value. Lower this if you have
difficulties successfully uploading a box on slow/unreliable connections.
