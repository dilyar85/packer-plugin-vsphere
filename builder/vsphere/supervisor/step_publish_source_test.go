package supervisor_test

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/hashicorp/packer-plugin-sdk/multistep"
	"github.com/hashicorp/packer-plugin-vsphere/builder/vsphere/supervisor"
	vmopv1alpha1 "github.com/vmware-tanzu/vm-operator/api/v1alpha1"
)

func TestPublishSource_Prepare(t *testing.T) {
	// Check error output when missing the required config.
	config := &supervisor.PublishSourceConfig{}
	var actualErrs []error

	if actualErrs = config.Prepare(); len(actualErrs) != 0 {
		t.Fatalf("Prepare should NOT fail: %v", actualErrs)
	}

	if config.WatchPublishTimeoutSec != supervisor.DefaultWatchPublishTimeoutSec {
		t.Fatalf("Default timeout should be %d, but got %d", supervisor.DefaultWatchPublishTimeoutSec,
			config.WatchPublishTimeoutSec)
	}
}

func newFakeVMPubReqObj(namespace, name string) *vmopv1alpha1.VirtualMachinePublishRequest {
	return &vmopv1alpha1.VirtualMachinePublishRequest{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: vmopv1alpha1.VirtualMachinePublishRequestSpec{
			TTLSecondsAfterFinished: pointer.Int64(supervisor.DefaultTTLSecondsAfterFinished),
			Target: vmopv1alpha1.VirtualMachinePublishRequestTarget{
				Location: vmopv1alpha1.VirtualMachinePublishRequestTargetLocation{
					Name: "test-publish-location-name",
				},
			},
		},
	}
}

func TestStepPublishSource_Run_Skip(t *testing.T) {
	// Initialize the step with required configs with `publish_location_name` set to empty
	config := &supervisor.PublishSourceConfig{
		WatchPublishTimeoutSec: 5,
	}
	step := &supervisor.StepPublishSource{
		Config: config,
	}
	testPublishLocationName := ""
	testSourceName := "test-source-name"
	testNamespace := "test-namespace"
	testKubeClient := newFakeKubeClient()

	// Set up required state for running this step.
	state := newBasicTestState(new(bytes.Buffer))
	state.Put(supervisor.StateKeyPublishLocationName, testPublishLocationName)
	state.Put(supervisor.StateKeySourceName, testSourceName)
	state.Put(supervisor.StateKeySupervisorNamespace, testNamespace)
	state.Put(supervisor.StateKeyKubeClient, testKubeClient)

	ctx := context.TODO()
	action := step.Run(ctx, state)

	if action != multistep.ActionContinue {
		if rawErr, ok := state.GetOk("error"); ok {
			t.Errorf("Error from running the step: %s", rawErr.(error))
		}
		t.Fatal("Step should continue")
	}
}

func TestStepPublishSource_Run(t *testing.T) {
	// Initialize the step with required configs.
	config := &supervisor.PublishSourceConfig{
		WatchPublishTimeoutSec: 5,
	}
	step := &supervisor.StepPublishSource{
		Config: config,
	}

	testPublishLocationName := "test-publish-location-name"
	testSourceName := "test-source-name"
	testNamespace := "test-namespace"
	testPublishRequestName := "test-publish-request-name"
	testTTLSecondsAfterFinished := pointer.Int64(60)
	VMPublishReqObj := newFakeVMPubReqObj(testNamespace, testPublishRequestName)
	testKubeClient := newFakeKubeClient(VMPublishReqObj)
	testImageName := "test-image-name"

	testWriter := new(bytes.Buffer)

	// Set up required state for running this step.
	state := newBasicTestState(testWriter)
	state.Put(supervisor.StateKeyPublishLocationName, testPublishLocationName)
	state.Put(supervisor.StateKeySourceName, testSourceName)
	state.Put(supervisor.StateKeySupervisorNamespace, testNamespace)
	state.Put(supervisor.StateKeyKubeClient, testKubeClient)

	ctx := context.TODO()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		action := step.Run(ctx, state)
		if action == multistep.ActionHalt {
			if rawErr, ok := state.GetOk("error"); ok {
				t.Errorf("Error from running the step: %s", rawErr.(error))
			}
			t.Errorf("Step should NOT halt")
		}

		// check if the VirtualMachinePublishRequest object is created with the expected spec.
		objKey := client.ObjectKey{
			Name:      testPublishRequestName,
			Namespace: testNamespace,
		}

		if err := testKubeClient.Get(ctx, objKey, VMPublishReqObj); err != nil {
			t.Errorf("Failed to get the expected VirtualMachinePublishRequest object, err: %s", err.Error())
		}
		if VMPublishReqObj.Name != testPublishRequestName {
			t.Errorf("Expected VirtualMachinePublishRequest name to be '%s', got '%s'", testPublishRequestName, VMPublishReqObj.Name)
		}
		if VMPublishReqObj.Namespace != testNamespace {
			t.Errorf("Expected VirtualMachinePublishRequest namespace to be '%s', got '%s'", testNamespace, VMPublishReqObj.Namespace)
		}
		if *VMPublishReqObj.Spec.TTLSecondsAfterFinished != *testTTLSecondsAfterFinished {
			t.Errorf("Expected VirtualMachinePublishRequest ttl to be '60', got '%d'", *VMPublishReqObj.Spec.TTLSecondsAfterFinished)
		}
		if VMPublishReqObj.Spec.Target.Location.Name != testPublishLocationName {
			t.Errorf("Expected VirtualMachinePublishRequest target location to be '%s', got '%s'", testPublishLocationName, VMPublishReqObj.Spec.Target.Location.Name)
		}

		expectedOutput := []string{
			"Publishing the source VM to \"test-publish-location-name\"",
			"Creating a VirtualMachinePublishRequest object",
			"Successfully created the VirtualMachinePublishRequest object",
			"Waiting for the VM publish request to complete...",
			"Successfully published the VM to image \"test-image-name\"",
		}
		checkOutputLines(t, testWriter, expectedOutput)
	}()

	// Wait for the watch to be established from Builder before updating the fake VirtualMachinePublishRequest resource below.
	for i := 0; i < step.Config.WatchPublishTimeoutSec; i++ {
		supervisor.Mu.Lock()
		if supervisor.IsWatchingVMPublish {
			supervisor.Mu.Unlock()
			break
		}
		supervisor.Mu.Unlock()
		time.Sleep(time.Second)
	}

	VMPublishReqObj.Status.Ready = false
	if err := testKubeClient.Update(ctx, VMPublishReqObj); err != nil {
		t.Errorf("Failed to update the VirtualMachinePublishRequest object, err: %s", err.Error())
	}

	VMPublishReqObj.Status.Ready = true
	VMPublishReqObj.Status.ImageName = testImageName
	if err := testKubeClient.Update(ctx, VMPublishReqObj); err != nil {
		t.Errorf("Failed to update the VirtualMachinePublishRequest object, err: %s", err.Error())
	}

	wg.Wait()
}
