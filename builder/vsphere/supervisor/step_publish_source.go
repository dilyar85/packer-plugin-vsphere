//go:generate packer-sdc struct-markdown
//go:generate packer-sdc mapstructure-to-hcl2 -type PublishSourceConfig

package supervisor

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/hashicorp/packer-plugin-sdk/multistep"
	vmopv1alpha1 "github.com/vmware-tanzu/vm-operator/api/v1alpha1"
)

const (
	DefaultWatchPublishTimeoutSec  = 600
	DefaultTTLSecondsAfterFinished = 60
)

var IsWatchingVMPublish bool

type PublishSourceConfig struct {
	// The name of the VM image will be published. If not specified, controller will set it to the source VM + "-image".
	PublishImageName string `mapstructure:"publish_image_name"`
	// The timeout in seconds to wait for the VM has been published successfully and the new VirtualMachineImage resource is ready.
	// If not specified, defaults to be `600`.
	WatchPublishTimeoutSec int `mapstructure:"watch_publish_timeout_sec"`
}

func (p *PublishSourceConfig) Prepare() []error {

	if p.WatchPublishTimeoutSec == 0 {
		p.WatchPublishTimeoutSec = DefaultWatchPublishTimeoutSec
	}

	return nil
}

type StepPublishSource struct {
	Config *PublishSourceConfig

	PublishLocationName, SourceName, Namespace string
	ShouldSkipPublishSource                    bool
	KubeWatchClient                            client.WithWatch
}

func (s *StepPublishSource) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	logger := state.Get("logger").(*PackerLogger)

	var err error
	defer func() {
		if err != nil {
			state.Put("error", err)
		}
	}()

	if err = s.initStep(state); err != nil {
		return multistep.ActionHalt
	}

	if s.ShouldSkipPublishSource {
		return multistep.ActionContinue
	}

	logger.Info("Publishing the source VM to %q", s.PublishLocationName)
	if err = s.createVMPublishRequest(ctx, logger); err != nil {
		return multistep.ActionHalt
	}

	_, err = s.watchVMPublish(ctx, logger)
	if err != nil {
		return multistep.ActionHalt
	}

	return multistep.ActionContinue
}

func (s *StepPublishSource) initStep(state multistep.StateBag) error {
	if err := CheckRequiredStates(state,
		StateKeyPublishLocationName,
		StateKeySourceName,
		StateKeySupervisorNamespace,
		StateKeyKubeClient,
	); err != nil {
		return err
	}

	var (
		ok                                         bool
		publishLocationName, sourceName, namespace string
		kubeWatchClient                            client.WithWatch
	)

	if publishLocationName, ok = state.Get(StateKeyPublishLocationName).(string); !ok {
		return fmt.Errorf("failed to cast %s to type string", StateKeyPublishLocationName)
	}
	if sourceName, ok = state.Get(StateKeySourceName).(string); !ok {
		return fmt.Errorf("failed to cast %s to type string", StateKeySourceName)
	}
	if namespace, ok = state.Get(StateKeySupervisorNamespace).(string); !ok {
		return fmt.Errorf("failed to cast %s to type string", StateKeySupervisorNamespace)
	}
	if kubeWatchClient, ok = state.Get(StateKeyKubeClient).(client.WithWatch); !ok {
		return fmt.Errorf("failed to cast %s to type client.WithWatch", StateKeyKubeClient)
	}

	s.PublishLocationName = publishLocationName
	s.SourceName = sourceName
	s.Namespace = namespace
	s.KubeWatchClient = kubeWatchClient

	if s.PublishLocationName == "" {
		s.ShouldSkipPublishSource = true
	}

	return nil
}

func (s *StepPublishSource) createVMPublishRequest(ctx context.Context, logger *PackerLogger) error {
	logger.Info("Creating a VirtualMachinePublishRequest object")

	vmPublishReq := &vmopv1alpha1.VirtualMachinePublishRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.SourceName,
			Namespace: s.Namespace,
		},
		Spec: vmopv1alpha1.VirtualMachinePublishRequestSpec{
			TTLSecondsAfterFinished: pointer.Int64(DefaultTTLSecondsAfterFinished),
			Target: vmopv1alpha1.VirtualMachinePublishRequestTarget{
				Location: vmopv1alpha1.VirtualMachinePublishRequestTargetLocation{
					Name: s.PublishLocationName,
				},
			},
		},
	}

	// Set the PublishImageName if provided in configs.
	if s.Config.PublishImageName != "" {
		vmPublishReq.Spec.Target.Item.Name = s.Config.PublishImageName
	}

	err := s.KubeWatchClient.Create(ctx, vmPublishReq)
	if err != nil {
		logger.Error("Failed to create the VirtualMachinePublishRequest object")
		return err
	}

	logger.Info("Successfully created the VirtualMachinePublishRequest object")
	return nil
}

func (s *StepPublishSource) watchVMPublish(ctx context.Context, logger *PackerLogger) (string, error) {
	vmPublishReqWatch, err := s.KubeWatchClient.Watch(ctx, &vmopv1alpha1.VirtualMachinePublishRequestList{}, &client.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("metadata.name", s.SourceName),
		Namespace:     s.Namespace,
	})

	if err != nil {
		logger.Error("Failed to watch the VirtualMachinePublishRequest object in Supervisor cluster")
		return "", err
	}

	defer func() {
		vmPublishReqWatch.Stop()

		Mu.Lock()
		IsWatchingVMPublish = false
		Mu.Unlock()
	}()

	Mu.Lock()
	IsWatchingVMPublish = true
	Mu.Unlock()

	for {
		select {
		case event := <-vmPublishReqWatch.ResultChan():
			if event.Object == nil {
				return "", fmt.Errorf("watch VirtualMachinePublishRequest event object is nil")
			}

			vmPublishReqObj, ok := event.Object.(*vmopv1alpha1.VirtualMachinePublishRequest)
			if !ok {
				return "", fmt.Errorf("failed to convert the watch VirtualMachinePublishRequest event object")
			}

			if vmPublishReqObj.Status.Ready {
				logger.Info("Successfully published the VM to image %q", vmPublishReqObj.Status.ImageName)
				return vmPublishReqObj.Status.ImageName, nil
			} else {
				logger.Info("Waiting for the VM publish request to complete...")
			}

		case <-time.After(time.Duration(s.Config.WatchPublishTimeoutSec) * time.Second):
			return "", fmt.Errorf("timed out watching for VirtualMachinePublishRequest object to complete")
		}
	}
}

// Cleanup This is a no-op cleanup as the DefaultTTLSecondsAfterFinished was set when creating the VirtualMachinePublishRequest resource,
// it will be automatically deleted DefaultTTLSecondsAfterFinished seconds after finishing its work.
func (s *StepPublishSource) Cleanup(bag multistep.StateBag) {}
