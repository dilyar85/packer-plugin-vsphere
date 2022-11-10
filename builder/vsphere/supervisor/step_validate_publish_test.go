package supervisor_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/packer-plugin-sdk/multistep"
	"github.com/hashicorp/packer-plugin-vsphere/builder/vsphere/supervisor"
	vmopv1alpha1 "github.com/vmware-tanzu/vm-operator/api/v1alpha1"
	imgregv1a1 "gitlab.eng.vmware.com/core-build/image-registry-operator-api/api/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	TestNamespace           = "test-namespace"
	TestPublishLocationName = "test-publish-location-name"
)

func TestValidate_Prepare(t *testing.T) {
	config := &supervisor.ValidatePublishConfig{}
	var actualErr []error

	if actualErr = config.Prepare(); len(actualErr) != 0 {
		t.Fatalf("Prepare should not fail: %v", actualErr)
	}
}

func validatePublishConfig(publishLocationName string) *supervisor.ValidatePublishConfig {
	return &supervisor.ValidatePublishConfig{
		PublishLocationName: publishLocationName,
	}
}
func stepValidatePublish(publishLocationName string) *supervisor.StepValidatePublish {
	step := &supervisor.StepValidatePublish{
		Config: validatePublishConfig(publishLocationName),
	}
	return step
}

func TestValidate_Run_LocationEmpty(t *testing.T) {
	// Initialize the step with configs, `publish_location_name` not set.
	step := stepValidatePublish("")

	testWriter := new(bytes.Buffer)
	state := newBasicTestState(testWriter)

	ctx := context.TODO()
	action := step.Run(ctx, state)

	if action == multistep.ActionHalt {
		if rawErr, ok := state.GetOk("error"); ok {
			t.Errorf("Error from running the step: %s", rawErr.(error))
		}
		t.Fatal("Step should NOT halt")
	}
	expectedOutput := []string{
		"Validating VM publish location...",
		"VM publish step will be skipped as the `publish_location_name` config is not set",
	}
	checkOutputLines(t, testWriter, expectedOutput)
}

func TestValidate_RunHalt_LocationInvalid(t *testing.T) {
	// Initialize the step with configs, `publish_location_name` set.
	step := stepValidatePublish(TestPublishLocationName)

	// Set up required state for running this step.
	contentLibraryObj := newFakeContentLibrary()
	// Test content library doesn't exist
	contentLibraryObj.Name = "non-existing-content-library"
	kubeClient := newFakeValidateKubeClient(contentLibraryObj)

	testWriter := new(bytes.Buffer)
	state := newBasicTestState(testWriter)

	state.Put(supervisor.StateKeyKubeClient, kubeClient)
	state.Put(supervisor.StateKeySupervisorNamespace, TestNamespace)
	ctx := context.TODO()

	action := step.Run(ctx, state)
	if action != multistep.ActionHalt {
		t.Fatal("Step should halt")
	}

	expectedError := fmt.Sprintf("%q not found", TestPublishLocationName)
	if rawErr, ok := state.GetOk("error"); ok {
		if !strings.Contains(rawErr.(error).Error(), expectedError) {
			t.Fatalf("Expected error contains %v, but got %v", expectedError, rawErr.(error).Error())
		}
	}

	// Test content library exists, but not writable
	contentLibraryObj.Name = TestPublishLocationName
	contentLibraryObj.Spec.Writable = false
	kubeClient = newFakeValidateKubeClient(contentLibraryObj)

	state.Put(supervisor.StateKeyKubeClient, kubeClient)

	action = step.Run(ctx, state)
	if action != multistep.ActionHalt {
		t.Fatal("Step should halt")
	}

	expectedOutput := []string{
		"Validating VM publish location...",
		"Validating VM publish location...",
	}
	checkOutputLines(t, testWriter, expectedOutput)

	expectedError = fmt.Sprintf("publish location %q is not writable", TestPublishLocationName)
	if rawErr, ok := state.GetOk("error"); ok {
		if rawErr.(error).Error() != expectedError {
			t.Fatalf("Expected error is %v, but got %v", expectedError, rawErr.(error).Error())
		}
	}
}

func TestValidate_Run(t *testing.T) {
	// Initialize the step with configs, `publish_location_name` set.
	step := stepValidatePublish(TestPublishLocationName)

	// Set up required state for running this step.
	contentLibraryObj := newFakeContentLibrary()
	kubeClient := newFakeValidateKubeClient(contentLibraryObj)
	testWriter := new(bytes.Buffer)

	state := newBasicTestState(testWriter)
	state.Put(supervisor.StateKeyKubeClient, kubeClient)
	state.Put(supervisor.StateKeySupervisorNamespace, TestNamespace)

	ctx := context.TODO()
	action := step.Run(ctx, state)
	if action == multistep.ActionHalt {
		if rawErr, ok := state.GetOk("error"); ok {
			t.Errorf("Error from running the step: %s", rawErr.(error))
		}
		t.Fatal("Step should NOT halt")
	}

	expectedOutput := []string{
		"Validating VM publish location...",
	}
	checkOutputLines(t, testWriter, expectedOutput)
}

func newFakeValidateKubeClient(initObjs ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	_ = imgregv1a1.AddToScheme(scheme)
	_ = vmopv1alpha1.AddToScheme(scheme)

	return fake.NewClientBuilder().WithObjects(initObjs...).WithScheme(scheme).Build()
}

func newFakeContentLibrary() *imgregv1a1.ContentLibrary {
	return &imgregv1a1.ContentLibrary{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: TestNamespace,
			Name:      TestPublishLocationName,
		},
		Spec: imgregv1a1.ContentLibrarySpec{
			Writable: true,
		},
	}
}
