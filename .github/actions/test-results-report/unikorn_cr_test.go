package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCollectUnikornCRContextsSummarizesKubectlOutput(t *testing.T) {
	previousRunner := runKubectlGet
	var receivedArgs []string
	runKubectlGet = func(_ context.Context, args ...string) ([]byte, error) {
		receivedArgs = append([]string{}, args...)
		return []byte(`{
  "apiVersion": "v1",
  "kind": "List",
  "items": [{
    "apiVersion": "region.unikorn-cloud.org/v1alpha1",
    "kind": "Network",
    "metadata": {
      "namespace": "unikorn",
      "name": "network-123",
      "ownerReferences": [{"kind": "Region", "name": "region-1"}]
    },
    "status": {
      "phase": "Error",
      "provisioningStatus": "failed",
      "conditions": [{
        "type": "Ready",
        "status": "False",
        "reason": "VLANExhausted",
        "message": "vlan ids exhausted"
      }]
    }
  }, {
    "apiVersion": "region.unikorn-cloud.org/v1alpha1",
    "kind": "Network",
    "metadata": {"namespace": "unikorn", "name": "other-network"},
    "status": {"phase": "Provisioned"}
  }]
}`), nil
	}
	defer func() {
		runKubectlGet = previousRunner
	}()

	enrichment := collectUnikornCRContexts(context.Background(), Config{
		UnikornCRTimeout: 5 * time.Second,
	}, []UnikornCRPlannedQuery{{
		FailureRef:  "f1",
		TestName:    "creates network",
		BackendArea: "network",
		Resource:    "networks.region.unikorn-cloud.org",
		Name:        "network-123",
		Reason:      "Network reached Error.",
		Confidence:  "high",
	}})

	if !reflect.DeepEqual(receivedArgs, []string{"get", "networks.region.unikorn-cloud.org", "-A", "--field-selector", "metadata.name=network-123", "-o", "json"}) {
		t.Fatalf("kubectl args = %#v", receivedArgs)
	}
	if len(enrichment.Contexts) != 1 {
		t.Fatalf("contexts = %+v", enrichment.Contexts)
	}
	context := enrichment.Contexts[0]
	if context.Error != "" || context.ResultCount != 1 || len(context.Objects) != 1 {
		t.Fatalf("unexpected context: %+v", context)
	}
	object := context.Objects[0]
	if object.Kind != "Network" ||
		object.Namespace != "unikorn" ||
		object.Name != "network-123" ||
		object.Phase != "Error" ||
		object.ProvisioningState != "failed" ||
		len(object.Conditions) != 1 ||
		!strings.Contains(object.Conditions[0], "VLANExhausted") ||
		len(object.OwnerRefs) != 1 ||
		object.OwnerRefs[0] != "Region/region-1" {
		t.Fatalf("unexpected object summary: %+v", object)
	}
	if signal := unikornCRSignalSummary(context); !strings.Contains(signal, "Network/network-123") || !strings.Contains(signal, "phase=Error") || !strings.Contains(signal, "VLANExhausted") {
		t.Fatalf("unexpected signal: %s", signal)
	}
}

func TestCollectUnikornCRContextsRecordsKubectlFailure(t *testing.T) {
	previousRunner := runKubectlGet
	runKubectlGet = func(_ context.Context, args ...string) ([]byte, error) {
		return nil, errors.New("forbidden")
	}
	defer func() {
		runKubectlGet = previousRunner
	}()

	enrichment := collectUnikornCRContexts(context.Background(), Config{}, []UnikornCRPlannedQuery{{
		FailureRef: "f1",
		Resource:   "networks.region.unikorn-cloud.org",
		Name:       "network-123",
	}})

	if len(enrichment.Contexts) != 1 {
		t.Fatalf("contexts = %+v", enrichment.Contexts)
	}
	if !strings.Contains(enrichment.Contexts[0].Error, "forbidden") {
		t.Fatalf("expected kubectl failure signal, got %+v", enrichment.Contexts[0])
	}
}

func TestCollectUnikornCRContextsSkipsUnsupportedLoadBalancerQueries(t *testing.T) {
	previousRunner := runKubectlGet
	runKubectlGet = func(_ context.Context, args ...string) ([]byte, error) {
		t.Fatalf("kubectl should not run for unsupported load balancer CR queries: %#v", args)
		return nil, nil
	}
	defer func() {
		runKubectlGet = previousRunner
	}()

	enrichment := collectUnikornCRContexts(context.Background(), Config{}, []UnikornCRPlannedQuery{{
		FailureRef: "f1",
		Resource:   "loadbalancers.region.unikorn-cloud.org",
		Name:       "lb-123",
	}})

	if len(enrichment.Contexts) != 0 {
		t.Fatalf("unsupported load balancer queries should be skipped, got %+v", enrichment.Contexts)
	}
}

func TestCollectUnikornCRContextsRejectsUnsafeQueries(t *testing.T) {
	previousRunner := runKubectlGet
	runKubectlGet = func(_ context.Context, args ...string) ([]byte, error) {
		t.Fatalf("kubectl should not run for unsafe queries: %#v", args)
		return nil, nil
	}
	defer func() {
		runKubectlGet = previousRunner
	}()

	enrichment := collectUnikornCRContexts(context.Background(), Config{}, []UnikornCRPlannedQuery{{
		FailureRef: "f1",
		Resource:   "pods",
		Name:       "pod-1",
	}})

	if len(enrichment.Contexts) != 1 {
		t.Fatalf("contexts = %+v", enrichment.Contexts)
	}
	if !strings.Contains(enrichment.Contexts[0].Error, "not allowed") {
		t.Fatalf("expected unsafe query error, got %+v", enrichment.Contexts[0])
	}
}

func TestRunUnikornCRCollectionModeWritesContextOutput(t *testing.T) {
	previousRunner := runKubectlGet
	runKubectlGet = func(_ context.Context, args ...string) ([]byte, error) {
		return []byte(`{"apiVersion":"region.unikorn-cloud.org/v1alpha1","kind":"Network","metadata":{"name":"network-123"},"status":{"phase":"Error"}}`), nil
	}
	defer func() {
		runKubectlGet = previousRunner
	}()

	tempDir := t.TempDir()
	planPath := filepath.Join(tempDir, "plan.json")
	contextPath := filepath.Join(tempDir, "context.json")
	outputPath := filepath.Join(tempDir, "github-output")
	if err := writeUnikornCRQueryPlan(planPath, []UnikornCRPlannedQuery{{
		FailureRef: "f1",
		Resource:   "networks.region.unikorn-cloud.org",
		Name:       "network-123",
	}}); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	t.Setenv("GITHUB_OUTPUT", outputPath)

	err := runUnikornCRCollectionMode(context.Background(), Config{
		UnikornCRPlanPath:    planPath,
		UnikornCRContextPath: contextPath,
	})
	if err != nil {
		t.Fatalf("runUnikornCRCollectionMode returned error: %v", err)
	}

	enrichment, err := readUnikornCRContext(contextPath)
	if err != nil {
		t.Fatalf("read context: %v", err)
	}
	if len(enrichment.Contexts) != 1 || enrichment.Contexts[0].ResultCount != 1 {
		t.Fatalf("unexpected context file: %+v", enrichment)
	}
	outputs, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read outputs: %v", err)
	}
	if !strings.Contains(string(outputs), "context-path="+contextPath) {
		t.Fatalf("outputs missing context path:\n%s", outputs)
	}
}

func TestRunUnikornCREnrichmentLoadsContextAndAttachesFailure(t *testing.T) {
	tempDir := t.TempDir()
	contextPath := filepath.Join(tempDir, "context.json")
	if err := writeUnikornCRContext(contextPath, &UnikornCREnrichment{Contexts: []UnikornCRContext{{
		FailureRef:  "f1",
		Resource:    "networks.region.unikorn-cloud.org",
		Name:        "network-123",
		ResultCount: 1,
	}}}); err != nil {
		t.Fatalf("write context: %v", err)
	}

	enrichment, err := runUnikornCREnrichment(Config{
		EnableUnikornCRs:     true,
		UnikornCRContextPath: contextPath,
		UnikornCRMaxFailures: 1,
	}, Analysis{
		Failures: []TestCase{{
			ID:   "network-create",
			Name: "creates network",
		}},
	})
	if err != nil {
		t.Fatalf("runUnikornCREnrichment returned error: %v", err)
	}
	if enrichment == nil || len(enrichment.Contexts) != 1 || enrichment.Contexts[0].Test == nil || enrichment.Contexts[0].Test.Name != "creates network" {
		t.Fatalf("context did not attach failure: %+v", enrichment)
	}
}
