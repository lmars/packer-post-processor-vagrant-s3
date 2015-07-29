Packer Vagrant S3 post-processor
================================

Uploads built Vagrant boxes to S3 and manages a manifest file for versioned boxes.

Installation
------------
Install the binary (you'll need ```git``` and ```go```):
 
```
$ go get github.com/lmars/packer-post-processor-vagrant-s3
```
Copy the plugin into packer.d directory:

```
$ mkdir $HOME/.packer.d/plugins
$ cp $GOPATH/bin/packer-post-processor-vagrant-s3 $HOME/.packer.d/plugins

```
Usage
-----

Add the post-processor to your packer template **after** the `vagrant` post-processor:

```
json
{
  "variables": {
    "version": "0.0.1"
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
        "manifest": "vagrant/manifest.json",
        "box_name": "my-cool-project",
        "box_dir":  "vagrant/boxes",
        "version":  "{{ user `version` }}"
      }
    ]
  ]
}
```
**NOTE:** The post-processors must be a **nested array** (i.e.: a Packer sequence definition) so that they run in order. See the [Packer template documentation](http://www.packer.io/docs/templates/post-processors.html) for more information.

The above will result in the following object created in S3, a manifest:

```
https://s3.amazonaws.com/my-s3-bucket/vagrant/manifest.json
```
and a box:

```
https://s3.amazonaws.com/my-s3-bucket/vagrant/boxes/my-cool-project/0.0.1/my-cool-project.box
  
```


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

### version

The version of the box you are uploading. The box will be uploaded to a S3 directory path that includes the version number (e.g. `vagrant/boxes/<version>`).

Only one box can be uploaded per provider for a given version. If you are building an updated box, you should bump this version, meaning users of your box will be made aware of the new version.

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

### access_key (optional)

Your AWS access key.

If not set, will use `AWS_ACCESS_KEY_ID` environment variable.

### secret_key (optional)

Your AWS secret key.

If not set, will use `AWS_SECRET_ACCESS_KEY` environment variable.
