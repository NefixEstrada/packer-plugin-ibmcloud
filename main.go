package main

import (
	"log"

	"github.com/hashicorp/packer-plugin-sdk/plugin"
	"github.com/ibmcloud/packer-builder-ibmcloud/builder/ibmcloud"
	"github.com/ibmcloud/packer-builder-ibmcloud/version"
)

func main() {
	log.Println("IBM Cloud Provider version", version.FormattedVersion, version.VersionPrerelease, version.GitCommit)

	pps := plugin.NewSet()
	pps.RegisterBuilder(plugin.DEFAULT_NAME, new(ibmcloud.Builder))
	err := pps.Run()
	if err != nil {
		panic(err)
	}
}
