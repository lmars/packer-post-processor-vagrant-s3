package main

import "github.com/hashicorp/packer-plugin-sdk/plugin"

func main() {
  pps := plugin.NewSet();
  pps.RegisterPostProcessor("vagrant-s3", new(PostProcessor))
  err := pps.Run()
	if err != nil {
		panic(err)
	}
}
