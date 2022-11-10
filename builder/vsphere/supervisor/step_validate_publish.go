//go:generate packer-sdc struct-markdown
//go:generate packer-sdc mapstructure-to-hcl2 -type ValidatePublishConfig

package supervisor

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/packer-plugin-sdk/multistep"

	vmopv1alpha1 "github.com/vmware-tanzu/vm-operator/api/v1alpha1"
	imgregv1a1 "gitlab.eng.vmware.com/core-build/image-registry-operator-api/api/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	StateKeyPublishLocationName = "publish_location_name"

	VmpubWebhookFeatureNotEnabledErrMsg = "WCP_VM_Image_Registry feature not enabled"
)

type ValidatePublishConfig struct {
	// The name of the target location(e.g. vSphere content library) that the image will be published to.
	PublishLocationName string `mapstructure:"publish_location_name"`
}

func (v *ValidatePublishConfig) Prepare() []error {
	return nil
}

type StepValidatePublish struct {
	Config *ValidatePublishConfig

	Namespace  string
	KubeClient client.Client
}

func (s *StepValidatePublish) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	logger := state.Get("logger").(*PackerLogger)
	logger.Info("Validating VM publish location...")
	var err error
	defer func() {
		if err != nil {
			state.Put("error", err)
		}
	}()

	state.Put(StateKeyPublishLocationName, s.Config.PublishLocationName)

	if s.Config.PublishLocationName == "" {
		logger.Info("VM publish step will be skipped as the `publish_location_name` config is not set")
		return multistep.ActionContinue
	}

	if err = s.initStep(state); err != nil {
		return multistep.ActionHalt
	}

	if err = s.isPublishVMFeatureEnabled(ctx, logger); err != nil {
		return multistep.ActionHalt
	}

	if err = s.isPublishLocationValid(ctx, logger); err != nil {
		return multistep.ActionHalt
	}

	return multistep.ActionContinue
}

func (s *StepValidatePublish) initStep(state multistep.StateBag) error {
	if err := CheckRequiredStates(state,
		StateKeySupervisorNamespace,
		StateKeyKubeClient,
	); err != nil {
		return err
	}

	var (
		ok         bool
		namespace  string
		kubeClient client.Client
	)
	if namespace, ok = state.Get(StateKeySupervisorNamespace).(string); !ok {
		return fmt.Errorf("failed to cast %q from state bag as type string", StateKeySupervisorNamespace)
	}
	if kubeClient, ok = state.Get(StateKeyKubeClient).(client.Client); !ok {
		return fmt.Errorf("failed to cast %q from state bag as type 'client.Client'", StateKeyKubeClient)
	}
	s.Namespace = namespace
	s.KubeClient = kubeClient
	return nil
}

func (s *StepValidatePublish) isPublishLocationValid(ctx context.Context, logger *PackerLogger) error {
	cl := &imgregv1a1.ContentLibrary{}
	if err := s.KubeClient.Get(ctx, client.ObjectKey{Name: s.Config.PublishLocationName, Namespace: s.Namespace}, cl); err != nil {
		return err
	}

	if !cl.Spec.Writable {
		return fmt.Errorf("publish location %q is not writable", s.Config.PublishLocationName)
	}

	return nil
}

func (s *StepValidatePublish) isPublishVMFeatureEnabled(ctx context.Context, logger *PackerLogger) error {
	vmPublishReq := &vmopv1alpha1.VirtualMachinePublishRequest{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    s.Namespace,
			GenerateName: "vmpub-",
		},
	}

	// Utilize the dry run mode to send a vmpub creation request to API-Server without storing the resource.
	err := client.NewDryRunClient(s.KubeClient).Create(ctx, vmPublishReq)
	if err != nil && strings.Contains(err.Error(), VmpubWebhookFeatureNotEnabledErrMsg) {
		logger.Error("Publish vm image feature is not enabled in this version of vSphere")
		return err
	}
	return nil
}

// Cleanup This is a no-op cleanup.
func (s *StepValidatePublish) Cleanup(state multistep.StateBag) {

}
