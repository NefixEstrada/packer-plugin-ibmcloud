//go:generate mapstructure-to-hcl2 -type Config
package ibmcloud

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/hashicorp/packer-plugin-sdk/common"
	"github.com/hashicorp/packer-plugin-sdk/communicator"
	"github.com/hashicorp/packer-plugin-sdk/multistep"
	"github.com/hashicorp/packer-plugin-sdk/multistep/commonsteps"
	"github.com/hashicorp/packer-plugin-sdk/packer"
	"github.com/hashicorp/packer-plugin-sdk/template/config"
	"github.com/hashicorp/packer-plugin-sdk/template/interpolate"
)

const BuilderId = "packer.ibmcloud"

type Config struct {
	common.PackerConfig   `mapstructure:",squash"`
	Comm                  communicator.Config `mapstructure:",squash"`
	config.KeyValueFilter `mapstructure:",squash"`

	Username            string   `mapstructure:"username"`
	APIKey              string   `mapstructure:"api_key"`
	ImageName           string   `mapstructure:"image_name"`
	ImageDescription    string   `mapstructure:"image_description"`
	ImageType           string   `mapstructure:"image_type"`
	BaseImageId         string   `mapstructure:"base_image_id"`
	BaseOsCode          string   `mapstructure:"base_os_code"`
	UploadToDatacenters []string `mapstructure:"upload_to_datacenters"`

	InstanceName                   string  `mapstructure:"instance_name"`
	InstanceDomain                 string  `mapstructure:"instance_domain"`
	InstanceFlavor                 string  `mapstructure:"instance_flavor"`
	InstanceLocalDiskFlag          bool    `mapstructure:"instance_local_disk_flag"`
	InstanceCpu                    int     `mapstructure:"instance_cpu"`
	InstanceMemory                 int64   `mapstructure:"instance_memory"`
	InstanceDiskCapacity           int     `mapstructure:"instance_disk_capacity"`
	DatacenterName                 string  `mapstructure:"datacenter_name"`
	PublicVlanId                   int64   `mapstructure:"public_vlan_id"`
	InstanceNetworkSpeed           int     `mapstructure:"instance_network_speed"`
	ProvisioningSshKeyId           int64   `mapstructure:"provisioning_ssh_key_id"`
	InstancePublicSecurityGroupIds []int64 `mapstructure:"public_security_groups"`

	RawStateTimeout string `mapstructure:"instance_state_timeout"`
	StateTimeout    time.Duration

	ctx interpolate.Context
}

// Image Types
//const IMAGE_TYPE_FLEX = "flex" //----NOT SUPPORTED
const IMAGE_TYPE_STANDARD = "standard"

// Builder represents a Packer Builder.
type Builder struct {
	config Config
	runner multistep.Runner
}

func (self *Builder) ConfigSpec() hcldec.ObjectSpec {
	return self.config.FlatMapstructure().HCL2Spec()
}

// Prepare processes the build configuration parameters.
func (self *Builder) Prepare(raws ...interface{}) (parms []string, param2 []string, retErr error) {
	err := config.Decode(&self.config, &config.DecodeOpts{
		Interpolate:        true,
		InterpolateContext: &self.config.ctx,
		InterpolateFilter:  &interpolate.RenderFilter{},
	}, raws...)

	if err != nil {
		return nil, nil, err
	}

	// Assign default values if possible
	if self.config.DatacenterName == "" {
		self.config.DatacenterName = "ams01"
	}

	if self.config.InstanceName == "" {
		self.config.InstanceName = fmt.Sprintf("packer-ibmcloud-%s", time.Now().Unix())
	}

	if self.config.InstanceDomain == "" {
		self.config.InstanceDomain = "defaultdomain.com"
	}

	if self.config.ImageDescription == "" {
		self.config.ImageDescription = "Instance snapshot. Generated by packer.io"
	}

	if self.config.ImageType == "" {
		self.config.ImageType = IMAGE_TYPE_STANDARD
	}

	if self.config.InstanceNetworkSpeed == 0 {
		self.config.InstanceNetworkSpeed = 10
	}

	if self.config.RawStateTimeout == "" {
		self.config.RawStateTimeout = "10m"
	}

	if self.config.Comm.Type == "winrm" {
		if self.config.Comm.WinRMUser == "" {
			self.config.Comm.WinRMUser = "Administrator"
		}
	} else if self.config.Comm.Type == "ssh" {
		if self.config.Comm.SSHUsername == "" {
			self.config.Comm.SSHUsername = "root"
		}
	}

	// Check for required configurations that will display errors if not set
	var byFlavor = true
	var errs *packer.MultiError
	errs = packer.MultiErrorAppend(errs, self.config.Comm.Prepare(&self.config.ctx)...)

	if self.config.InstanceCpu > 0 {
		byFlavor = false
	}

	if self.config.InstanceMemory > 0 {
		byFlavor = false
	}

	if self.config.InstanceDiskCapacity > 0 {
		byFlavor = false
	}

	if !byFlavor && self.config.InstanceFlavor != "" {
		errs = packer.MultiErrorAppend(
			errs, errors.New("instance_flavor must be specified without instance_cpu, instance_memory, and instance_disk_capacity"))
	} else if byFlavor && self.config.InstanceFlavor == "" {
		errs = packer.MultiErrorAppend(
			errs, errors.New("instance_flavor must be specified"))
	}

	if self.config.APIKey == "" {
		errs = packer.MultiErrorAppend(
			errs, errors.New("api_key or the SOFTLAYER_API_KEY environment variable must be specified"))
	}

	if self.config.Username == "" {
		errs = packer.MultiErrorAppend(
			errs, errors.New("username or the SOFTLAYER_USER_NAME environment variable must be specified"))
	}

	if self.config.ImageName == "" {
		errs = packer.MultiErrorAppend(
			errs, errors.New("image_name must be specified"))
	}

	if self.config.ImageType != IMAGE_TYPE_STANDARD {
		errs = packer.MultiErrorAppend(
			errs, fmt.Errorf("Unknown image_type '%s'. Must be 'standard'.", self.config.ImageType))
	}

	if self.config.BaseImageId == "" && self.config.BaseOsCode == "" {
		errs = packer.MultiErrorAppend(
			errs, errors.New("please specify base_image_id or base_os_code"))
	}

	if self.config.BaseImageId != "" && self.config.BaseOsCode != "" {
		errs = packer.MultiErrorAppend(
			errs, errors.New("please specify only one of base_image_id or base_os_code"))
	}

	stateTimeout, err := time.ParseDuration(self.config.RawStateTimeout)
	if err != nil {
		errs = packer.MultiErrorAppend(
			errs, fmt.Errorf("Failed parsing state_timeout: %s", err))
	}
	self.config.StateTimeout = stateTimeout

	//log.Println(common.ScrubConfig(self.config, self.config.APIKey, self.config.Username))

	if len(errs.Errors) > 0 {
		retErr = errors.New(errs.Error())
	}

	return nil, nil, retErr
}

// Run executes a SoftLayer Packer build and returns a packer.Artifact
// representing a SoftLayer machine image (standard).
// func (self *Builder) Run(ui packer.Ui, hook packer.Hook, cache packer.Cache) (packer.Artifact, error) {
func (self *Builder) Run(ctx context.Context, ui packer.Ui, hook packer.Hook) (packer.Artifact, error) {

	// Create the client
	client := SoftlayerClient{}.New(self.config.Username, self.config.APIKey)

	// Set up the state which is used to share state between the steps
	state := new(multistep.BasicStateBag)
	state.Put("config", self.config)
	state.Put("client", client)
	state.Put("hook", hook)
	state.Put("ui", ui)

	// Build the steps
	steps := []multistep.Step{}
	if self.config.Comm.Type == "winrm" {
		steps = []multistep.Step{
			new(stepCreateInstance),
			new(stepWaitforInstance),
			new(stepGrabPublicIP),
			&communicator.StepConnect{
				Config:      &self.config.Comm,
				Host:        winRMCommHost,
				WinRMConfig: winRMConfig,
			},
			new(stepWaitforInstance),
			new(commonsteps.StepProvision),
			new(stepCaptureImage),
		}
	} else if self.config.Comm.Type == "ssh" {
		steps = []multistep.Step{
			&stepCreateSshKey{
				PrivateKeyFile: string(self.config.Comm.SSHPrivateKey),
			},
			new(stepCreateInstance),
			new(stepWaitforInstance),
			new(stepGrabPublicIP),
			&communicator.StepConnect{
				Config:    &self.config.Comm,
				Host:      sshCommHost,
				SSHConfig: sshConfig,
			},
			new(commonsteps.StepProvision),
			new(stepCaptureImage),
		}
	}

	// Create the runner which will run the steps we just build
	self.runner = &multistep.BasicRunner{Steps: steps}
	self.runner.Run(ctx, state)

	// If there was an error, return that
	if rawErr, ok := state.GetOk("error"); ok {
		return nil, rawErr.(error)
	}

	if _, ok := state.GetOk("image_id"); !ok {
		log.Println("Failed to find image_id in state. Bug?")
		return nil, nil
	}

	// Create an artifact and return it
	artifact := &Artifact{
		imageName:      self.config.ImageName,
		imageId:        state.Get("image_id").(string),
		datacenterName: self.config.DatacenterName,
		client:         client,
	}

	return artifact, nil
}

// Cancel.
// func (self *Builder) Cancel() {
// 	if self.runner != nil {
// 		log.Println("Cancelling the step runner...")
// 		self.runner.Cancel()
// 	}
// 	fmt.Println("Canceling the builder")
// }
