package providers

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// mockEKSClient implements EKSDiscoveryAPI for testing.
type mockEKSClient struct {
	clusters       []string
	describeResult map[string]*eks.DescribeClusterOutput
	listErr        error
	describeErr    map[string]error
}

func (m *mockEKSClient) ListClusters(_ context.Context, _ *eks.ListClustersInput, _ ...func(*eks.Options)) (*eks.ListClustersOutput, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return &eks.ListClustersOutput{Clusters: m.clusters}, nil
}

func (m *mockEKSClient) DescribeCluster(_ context.Context, params *eks.DescribeClusterInput, _ ...func(*eks.Options)) (*eks.DescribeClusterOutput, error) {
	name := aws.ToString(params.Name)
	if m.describeErr != nil {
		if err, ok := m.describeErr[name]; ok {
			return nil, err
		}
	}
	if m.describeResult != nil {
		if out, ok := m.describeResult[name]; ok {
			return out, nil
		}
	}
	return &eks.DescribeClusterOutput{
		Cluster: &ekstypes.Cluster{
			Name:     aws.String(name),
			Status:   ekstypes.ClusterStatusActive,
			Version:  aws.String("1.28"),
			Endpoint: aws.String("https://" + name + ".eks.amazonaws.com"),
		},
	}, nil
}

// mockSTSClient implements STSCallerIdentityAPI for testing.
type mockSTSClient struct {
	account string
	err     error
}

func (m *mockSTSClient) GetCallerIdentity(_ context.Context, _ *sts.GetCallerIdentityInput, _ ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &sts.GetCallerIdentityOutput{
		Account: aws.String(m.account),
	}, nil
}

func TestDiscoverEKSClusters_DefaultIdentity(t *testing.T) {
	mock := &mockEKSClient{
		clusters: []string{"prod-eu", "staging-us"},
	}
	stsMock := &mockSTSClient{account: "123456789012"}

	results, err := discoverEKSClustersWithFactory(
		context.Background(),
		nil, // no role ARNs → default identity
		"us-east-1",
		func(_ aws.Config) EKSDiscoveryAPI { return mock },
		func(_ aws.Config) STSCallerIdentityAPI { return stsMock },
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 clusters, got %d", len(results))
	}

	for _, r := range results {
		if r.Account != "123456789012" {
			t.Errorf("expected account 123456789012, got %q", r.Account)
		}
		if r.Region != "us-east-1" {
			t.Errorf("expected region us-east-1, got %q", r.Region)
		}
		if r.Status != string(ekstypes.ClusterStatusActive) {
			t.Errorf("expected status ACTIVE, got %q", r.Status)
		}
		if r.K8sVersion != "1.28" {
			t.Errorf("expected version 1.28, got %q", r.K8sVersion)
		}
	}
}

func TestDiscoverEKSClusters_DescribeError(t *testing.T) {
	mock := &mockEKSClient{
		clusters:    []string{"good-cluster", "bad-cluster"},
		describeErr: map[string]error{"bad-cluster": fmt.Errorf("access denied")},
	}
	stsMock := &mockSTSClient{account: "111111111111"}

	results, err := discoverEKSClustersWithFactory(
		context.Background(),
		nil,
		"eu-west-1",
		func(_ aws.Config) EKSDiscoveryAPI { return mock },
		func(_ aws.Config) STSCallerIdentityAPI { return stsMock },
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// good-cluster should have no error
	if results[0].Error != "" {
		t.Errorf("expected no error for good-cluster, got %q", results[0].Error)
	}
	// bad-cluster should have error set
	if results[1].Error == "" {
		t.Error("expected error for bad-cluster")
	}
	if results[1].Status != "UNKNOWN" {
		t.Errorf("expected UNKNOWN status for bad-cluster, got %q", results[1].Status)
	}
}

func TestDiscoverEKSClusters_NoClusters(t *testing.T) {
	mock := &mockEKSClient{clusters: []string{}}
	stsMock := &mockSTSClient{account: "000000000000"}

	results, err := discoverEKSClustersWithFactory(
		context.Background(),
		nil,
		"us-west-2",
		func(_ aws.Config) EKSDiscoveryAPI { return mock },
		func(_ aws.Config) STSCallerIdentityAPI { return stsMock },
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 clusters, got %d", len(results))
	}
}

func TestDiscoverEKSClusters_ListError(t *testing.T) {
	mock := &mockEKSClient{listErr: fmt.Errorf("unauthorized")}
	stsMock := &mockSTSClient{account: "000000000000"}

	_, err := discoverEKSClustersWithFactory(
		context.Background(),
		nil,
		"us-east-1",
		func(_ aws.Config) EKSDiscoveryAPI { return mock },
		func(_ aws.Config) STSCallerIdentityAPI { return stsMock },
	)
	if err == nil {
		t.Fatal("expected error when ListClusters fails")
	}
}

func TestDiscoverEKSClusters_STSError(t *testing.T) {
	mock := &mockEKSClient{clusters: []string{"test"}}
	stsMock := &mockSTSClient{err: fmt.Errorf("AccessDenied: not authorized to perform: sts:AssumeRole")}

	_, err := discoverEKSClustersWithFactory(
		context.Background(),
		[]string{"arn:aws:iam::999:role/Bad"},
		"us-east-1",
		func(_ aws.Config) EKSDiscoveryAPI { return mock },
		func(_ aws.Config) STSCallerIdentityAPI { return stsMock },
	)
	if err == nil {
		t.Fatal("expected error when STS fails")
	}
}

func TestDiscoverEKSClusters_MultiRole_PartialFailure(t *testing.T) {
	goodMock := &mockEKSClient{clusters: []string{"cluster-a"}}
	goodSTS := &mockSTSClient{account: "111111111111"}
	badSTS := &mockSTSClient{err: fmt.Errorf("AccessDenied: AssumeRole failed")}

	callCount := 0
	results, err := discoverEKSClustersWithFactory(
		context.Background(),
		[]string{"arn:aws:iam::111:role/Good", "arn:aws:iam::222:role/Bad"},
		"us-east-1",
		func(_ aws.Config) EKSDiscoveryAPI { return goodMock },
		func(_ aws.Config) STSCallerIdentityAPI {
			callCount++
			if callCount == 1 {
				return goodSTS
			}
			return badSTS
		},
	)

	// Should return partial results with error.
	if err == nil {
		t.Fatal("expected partial error")
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 cluster from successful scan, got %d", len(results))
	}
	if results[0].Name != "cluster-a" {
		t.Errorf("expected cluster-a, got %q", results[0].Name)
	}
}

func TestDiscoverEKSClusters_ClusterStatus(t *testing.T) {
	mock := &mockEKSClient{
		clusters: []string{"active-cluster", "creating-cluster"},
		describeResult: map[string]*eks.DescribeClusterOutput{
			"active-cluster": {
				Cluster: &ekstypes.Cluster{
					Name:     aws.String("active-cluster"),
					Status:   ekstypes.ClusterStatusActive,
					Version:  aws.String("1.29"),
					Endpoint: aws.String("https://active.eks.amazonaws.com"),
				},
			},
			"creating-cluster": {
				Cluster: &ekstypes.Cluster{
					Name:    aws.String("creating-cluster"),
					Status:  ekstypes.ClusterStatusCreating,
					Version: aws.String("1.29"),
				},
			},
		},
	}
	stsMock := &mockSTSClient{account: "123456789012"}

	results, err := discoverEKSClustersWithFactory(
		context.Background(),
		nil,
		"us-east-1",
		func(_ aws.Config) EKSDiscoveryAPI { return mock },
		func(_ aws.Config) STSCallerIdentityAPI { return stsMock },
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 clusters, got %d", len(results))
	}
	if results[0].Status != "ACTIVE" {
		t.Errorf("expected ACTIVE, got %q", results[0].Status)
	}
	if results[1].Status != "CREATING" {
		t.Errorf("expected CREATING, got %q", results[1].Status)
	}
}

func TestTrustPolicyFix(t *testing.T) {
	fix := trustPolicyFix("arn:aws:iam::123:role/MyRole")
	if fix == "" {
		t.Error("expected non-empty trust policy fix text")
	}
	if !strings.Contains(fix, "arn:aws:iam::123:role/MyRole") {
		t.Error("expected fix to contain the role ARN")
	}
	if !strings.Contains(fix, "sts:AssumeRole") {
		t.Error("expected fix to mention sts:AssumeRole")
	}
}

func TestIsAssumeRoleError(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"AccessDenied: not authorized", true},
		{"AssumeRole failed", true},
		{"network timeout", false},
		{"not authorized to perform: sts:AssumeRole on resource", true},
	}
	for _, tt := range tests {
		got := isAssumeRoleError(fmt.Errorf("%s", tt.msg))
		if got != tt.want {
			t.Errorf("isAssumeRoleError(%q) = %v, want %v", tt.msg, got, tt.want)
		}
	}
}

