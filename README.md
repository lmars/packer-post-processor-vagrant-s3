Packer Vagrant S3 post-processor
================================

Uploads built Vagrant boxes to S3 and manages a manifest file for versioned boxes.

Usage
-----

Add the post-processor to your packer template **after** the `vagrant` post-processor:

```json
{
  "variables": {
    "version": "0.0.1"
  },
  "builders": [ ],
  "provisioners": [ ],
  "post-processors": [
    [
      {
        "type": "vagrant"
      },
      {
        "type":     "vagrant-s3",
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

Installation
------------

Install the binary:

```
$ go get github.com/lmars/packer-post-processor-vagrant-s3
```

Add the post-processor to `~/.packerconfig`:

```json
{
  "post-processors": {
    "vagrant-s3": "packer-post-processor-vagrant-s3"
  }
}
```

Configuration Options
---------------------

### bucket

The S3 bucket you want to upload the box and manifest to.

### manifest

The path to the manifest file in your bucket (e.g. `vagrant/manifest.json`).

This controls what users of your box will set `vm.config.box_url` to in their `Vagrantfile` (e.g. `https://s3.amazonaws.com/my-bucket/vagrant/manifest.json`).

### box_name

The name of your box.

This is what users of your box will set `vm.config.box` to in their `Vagrantfile`.

### box_dir

The path to a directory in your bucket to store boxes in (e.g. `vagrant/boxes`).

### version

The version of the box you are uploading.

Only one box can be uploaded per provider for a given version. If you are building an updated box, you should bump this version, meaning users of your box will be made aware of the new version.

### access_key (optional)

Your AWS access key.

If not set, will use `AWS_ACCESS_KEY_ID` environment variable.

### secret_key (optional)

Your AWS secret key.

If not set, will use `AWS_SECRET_ACCESS_KEY` environment variable.
