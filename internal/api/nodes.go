package api

import (
	"context"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type nodeInfo struct {
	Name              string `json:"name"`
	Status            string `json:"status"` // "Ready" or "NotReady"
	InstanceType      string `json:"instance_type"`
	Architecture      string `json:"architecture"`
	OS                string `json:"os"`
	CapacityCPU       string `json:"capacity_cpu"`
	CapacityMemory    string `json:"capacity_memory"`
	AllocatableCPU    string `json:"allocatable_cpu"`
	AllocatableMemory string `json:"allocatable_memory"`
}

func (s *Server) handleGetNodeInfo(w http.ResponseWriter, r *http.Request) {
	config, err := rest.InClusterConfig()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"nodes":   []nodeInfo{},
			"message": "Node info only available when running in-cluster",
		})
		return
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create Kubernetes client: "+err.Error())
		return
	}

	nodes, err := clientset.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list nodes: "+err.Error())
		return
	}

	result := make([]nodeInfo, 0, len(nodes.Items))
	for _, node := range nodes.Items {
		// Determine node ready status
		status := "NotReady"
		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
				status = "Ready"
				break
			}
		}

		info := nodeInfo{
			Name:         node.Name,
			Status:       status,
			InstanceType: node.Labels["node.kubernetes.io/instance-type"],
			Architecture: node.Status.NodeInfo.Architecture,
			OS:           node.Status.NodeInfo.OperatingSystem,
		}

		if cpu := node.Status.Capacity.Cpu(); cpu != nil {
			info.CapacityCPU = cpu.String()
		}
		if mem := node.Status.Capacity.Memory(); mem != nil {
			info.CapacityMemory = mem.String()
		}
		if cpu := node.Status.Allocatable.Cpu(); cpu != nil {
			info.AllocatableCPU = cpu.String()
		}
		if mem := node.Status.Allocatable.Memory(); mem != nil {
			info.AllocatableMemory = mem.String()
		}

		result = append(result, info)
	}

	readyCount := 0
	for _, n := range result {
		if n.Status == "Ready" {
			readyCount++
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"nodes":       result,
		"total":       len(result),
		"ready":       readyCount,
		"not_ready":   len(result) - readyCount,
	})
}
