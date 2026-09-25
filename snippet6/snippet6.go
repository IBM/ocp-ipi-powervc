// Copyright 2026 IBM Corp
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

// (cd snippet6/; /bin/rm -f go.*; go mod init example/user/snippet6; go mod tidy)
// (set -euo pipefail; cd snippet6/; go build ./...; ./snippet6 --kubeconfig ~/.kube/config --namespace default --name my-pvc --size 10 --shouldDebug true)
// (set -euo pipefail; cd snippet6/; go build ./...; ./snippet6 --kubeconfig ~/.kube/config --namespace default --name my-pvc --size 10 --storageClass gp3-csi --shouldDebug true)
// (cd snippet6/; /bin/rm -f go.*)

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"

	"github.com/sirupsen/logrus"
)

const (
	// defaultTimeout is the default timeout for operations
	defaultTimeout = 15 * time.Minute

	// Backoff configuration constants for retry logic
	defaultBackoffDuration = 1 * time.Minute
	defaultBackoffFactor   = 1.1
	defaultBackoffSteps    = math.MaxInt32

	// Resource polling wait configuration
	resourceWaitDuration = 5 * time.Second

	// Default volume size in GiB
	defaultVolumeSize = 10

	// Default namespace
	defaultNamespace = "default"

	// Default consumer pod image
	defaultPodImage = "registry.redhat.io/ubi9/ubi-minimal:latest"
)

var (
	// log is the global logger instance used throughout the application
	log *logrus.Logger
)

// leftInContext returns the remaining time in the context
func leftInContext(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return math.MaxInt64
	}
	return time.Until(deadline)
}

// parseBoolFlag converts a string flag value to boolean, accepting "true", "false",
// "1", "0", "yes", "no", "y", "n" (case-insensitive).
func parseBoolFlag(value, flagName string) (bool, error) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "true", "1", "yes", "y":
		return true, nil
	case "false", "0", "no", "n":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be 'true' or 'false', got: %q", flagName, value)
	}
}

// initLogger creates a configured logger based on debug flag.
// When debug is true, logs are written to stderr; otherwise, they are discarded.
func initLogger(debug bool) *logrus.Logger {
	out := io.Discard
	if debug {
		out = os.Stderr
	}

	return &logrus.Logger{
		Out: out,
		Formatter: &logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "2006-01-02 15:04:05",
		},
		Level: logrus.DebugLevel,
	}
}

// createDefaultBackoff creates a standard backoff configuration for retry logic.
func createDefaultBackoff(ctx context.Context) wait.Backoff {
	return wait.Backoff{
		Duration: defaultBackoffDuration,
		Factor:   defaultBackoffFactor,
		Cap:      leftInContext(ctx),
		Steps:    defaultBackoffSteps,
	}
}

// createResourceWaitBackoff creates a backoff configuration for polling resource status.
func createResourceWaitBackoff(ctx context.Context) wait.Backoff {
	return wait.Backoff{
		Duration: resourceWaitDuration,
		Factor:   defaultBackoffFactor,
		Cap:      leftInContext(ctx),
		Steps:    defaultBackoffSteps,
	}
}

// getKubernetesClient creates and returns a Kubernetes/OpenShift clientset.
// It checks the provided kubeconfig path, KUBECONFIG environment variable,
// in-cluster configuration, and default ~/.kube/config path.
//
// Parameters:
//   - ctx:            Context for cancellation and timeout control
//   - kubeconfigPath: Explicit path to kubeconfig file (optional)
//
// Returns:
//   - kubernetes.Interface: Initialized Kubernetes clientset
//   - error: Any error encountered during client creation
func getKubernetesClient(ctx context.Context, kubeconfigPath string) (kubernetes.Interface, error) {
	var config *rest.Config
	var err error

	if kubeconfigPath != "" {
		log.Debugf("getKubernetesClient: loading config from explicit path %q", kubeconfigPath)
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load kubeconfig from %q: %w", kubeconfigPath, err)
		}
	} else if kubeconfigEnv := os.Getenv("KUBECONFIG"); kubeconfigEnv != "" {
		log.Debugf("getKubernetesClient: loading config from KUBECONFIG environment variable %q", kubeconfigEnv)
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfigEnv)
		if err != nil {
			return nil, fmt.Errorf("failed to load kubeconfig from KUBECONFIG env %q: %w", kubeconfigEnv, err)
		}
	} else if inClusterConfig, inClusterErr := rest.InClusterConfig(); inClusterErr == nil {
		log.Debugf("getKubernetesClient: using in-cluster configuration")
		config = inClusterConfig
	} else {
		home := homedir.HomeDir()
		if home == "" {
			return nil, fmt.Errorf("could not locate home directory and no kubeconfig specified")
		}
		defaultPath := filepath.Join(home, ".kube", "config")
		log.Debugf("getKubernetesClient: loading config from default path %q", defaultPath)
		config, err = clientcmd.BuildConfigFromFlags("", defaultPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load default kubeconfig from %q: %w", defaultPath, err)
		}
	}

	var clientset *kubernetes.Clientset
	backoff := createDefaultBackoff(ctx)

	err = wait.ExponentialBackoffWithContext(ctx, backoff, func(context.Context) (bool, error) {
		var err2 error
		log.Debugf("getKubernetesClient: duration = %v, creating clientset", leftInContext(ctx))
		clientset, err2 = kubernetes.NewForConfig(config)
		if err2 != nil {
			log.Debugf("getKubernetesClient: Error: NewForConfig returns error %v", err2)
			return false, err2
		}
		return true, nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes clientset: %w", err)
	}

	return clientset, nil
}

// createPVC creates a PersistentVolumeClaim in OpenShift/Kubernetes and waits for
// it to reach the "Bound" status (or remain in "Pending" when WaitForFirstConsumer is used).
//
// Parameters:
//   - ctx:          Context for cancellation and timeout control
//   - kubeClient:   Authenticated Kubernetes clientset
//   - namespace:    Target namespace/project
//   - pvcName:      Name for the new PVC
//   - sizeGiB:      Size of the volume in GiB
//   - storageClass: Optional storage class name (pass "" to use cluster default)
//   - accessMode:   Access mode (e.g., ReadWriteOnce)
//
// Returns:
//   - *corev1.PersistentVolumeClaim: The created PVC
//   - error: Any error encountered during creation or the wait
func createPVC(ctx context.Context, kubeClient kubernetes.Interface, namespace string, pvcName string, sizeGiB int, storageClass string, accessMode corev1.PersistentVolumeAccessMode) (*corev1.PersistentVolumeClaim, error) {
	if namespace == "" {
		return nil, fmt.Errorf("namespace cannot be empty")
	}
	if pvcName == "" {
		return nil, fmt.Errorf("PVC name cannot be empty")
	}
	if sizeGiB <= 0 {
		return nil, fmt.Errorf("volume size must be greater than 0, got: %d", sizeGiB)
	}

	quantity, err := resource.ParseQuantity(fmt.Sprintf("%dGi", sizeGiB))
	if err != nil {
		return nil, fmt.Errorf("invalid volume size %dGi: %w", sizeGiB, err)
	}

	pvcSpec := corev1.PersistentVolumeClaimSpec{
		AccessModes: []corev1.PersistentVolumeAccessMode{
			accessMode,
		},
		Resources: corev1.VolumeResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceStorage: quantity,
			},
		},
	}

	if storageClass != "" {
		pvcSpec.StorageClassName = &storageClass
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: namespace,
		},
		Spec: pvcSpec,
	}

	log.Debugf("createPVC: namespace=%s name=%s size=%dGi storageClass=%q accessMode=%s", namespace, pvcName, sizeGiB, storageClass, accessMode)
	log.Debugf("createPVC: calling PersistentVolumeClaims(%s).Create", namespace)

	createdPVC, err := kubeClient.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, pvc, metav1.CreateOptions{})
	if err != nil {
		if errors.IsAlreadyExists(err) {
			log.Debugf("createPVC: PVC %q already exists in namespace %q, attempting cleanup", pvcName, namespace)
			fmt.Printf("PersistentVolumeClaim %q already exists in namespace %q. Cleaning up previous PVC and associated pod...\n", pvcName, namespace)

			// Clean up any consumer pod that might be using the existing PVC
			podName := pvcName + "-consumer-pod"
			if errPod := deletePod(ctx, kubeClient, namespace, podName); errPod != nil {
				log.Debugf("createPVC: cleanup deletePod returned %v", errPod)
			}

			// Clean up the existing PVC
			if errPVC := deletePVC(ctx, kubeClient, namespace, pvcName); errPVC != nil {
				return nil, fmt.Errorf("failed to clean up existing PVC %q: %w", pvcName, errPVC)
			}
			fmt.Printf("Previous resources cleaned up successfully. Re-creating PersistentVolumeClaim %q...\n", pvcName)

			// Re-attempt creation
			createdPVC, err = kubeClient.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, pvc, metav1.CreateOptions{})
			if err != nil {
				return nil, fmt.Errorf("failed to re-create PVC %q in namespace %q: %w", pvcName, namespace, err)
			}
		} else {
			return nil, fmt.Errorf("failed to create PVC %q in namespace %q: %w", pvcName, namespace, err)
		}
	}

	log.Debugf("createPVC: PVC created with UID=%s, waiting for status", createdPVC.UID)

	// Poll until the PVC is Bound or Pending
	waitBackoff := createResourceWaitBackoff(ctx)

	err = wait.ExponentialBackoffWithContext(ctx, waitBackoff, func(context.Context) (bool, error) {
		current, err2 := kubeClient.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, pvcName, metav1.GetOptions{})
		if err2 != nil {
			log.Debugf("createPVC: Get returned error %v", err2)
			return false, nil
		}

		log.Debugf("createPVC: PVC %s phase=%s volumeName=%s", current.Name, current.Status.Phase, current.Spec.VolumeName)

		switch current.Status.Phase {
		case corev1.ClaimBound:
			createdPVC = current
			return true, nil
		case corev1.ClaimPending:
			createdPVC = current
			// If storage class uses WaitForFirstConsumer binding mode, PVC stays Pending until a Pod uses it.
			// Return success once it exists in Pending phase.
			return true, nil
		case corev1.ClaimLost:
			return false, fmt.Errorf("PVC %q entered Lost phase", pvcName)
		}

		return false, nil
	})

	if err != nil {
		return nil, fmt.Errorf("PVC %q did not reach ready state: %w", pvcName, err)
	}

	return createdPVC, nil
}

// waitForPVCBound polls the PVC until it reaches the Bound phase.
//
// Parameters:
//   - ctx:        Context for cancellation and timeout control
//   - kubeClient: Authenticated Kubernetes clientset
//   - namespace:  Namespace of the PVC
//   - pvcName:    Name of the PVC
//
// Returns:
//   - *corev1.PersistentVolumeClaim: The bound PVC
//   - error: Any error encountered during waiting
func waitForPVCBound(ctx context.Context, kubeClient kubernetes.Interface, namespace string, pvcName string) (*corev1.PersistentVolumeClaim, error) {
	var boundPVC *corev1.PersistentVolumeClaim
	waitBackoff := createResourceWaitBackoff(ctx)

	log.Debugf("waitForPVCBound: waiting for PVC %q in namespace %q to become Bound", pvcName, namespace)

	err := wait.ExponentialBackoffWithContext(ctx, waitBackoff, func(context.Context) (bool, error) {
		current, err := kubeClient.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, pvcName, metav1.GetOptions{})
		if err != nil {
			log.Debugf("waitForPVCBound: Get returned error %v", err)
			return false, nil
		}

		log.Debugf("waitForPVCBound: PVC %s phase=%s volumeName=%s", current.Name, current.Status.Phase, current.Spec.VolumeName)

		if current.Status.Phase == corev1.ClaimBound {
			boundPVC = current
			return true, nil
		}
		if current.Status.Phase == corev1.ClaimLost {
			return false, fmt.Errorf("PVC %q entered Lost phase", pvcName)
		}

		return false, nil
	})

	if err != nil {
		return nil, fmt.Errorf("timed out waiting for PVC %q to bind: %w", pvcName, err)
	}

	return boundPVC, nil
}

// createConsumerPod creates a Pod that mounts the specified PVC to trigger volume binding.
//
// Parameters:
//   - ctx:        Context for cancellation and timeout control
//   - kubeClient: Authenticated Kubernetes clientset
//   - namespace:  Target namespace/project
//   - podName:    Name for the consumer pod
//   - pvcName:    Name of the PVC to mount
//   - image:      Container image to run
//   - mountPath:  Mount path inside container
//
// Returns:
//   - *corev1.Pod: The created Pod
//   - error: Any error encountered during creation
func createConsumerPod(ctx context.Context, kubeClient kubernetes.Interface, namespace string, podName string, pvcName string, image string, mountPath string) (*corev1.Pod, error) {
	if namespace == "" {
		return nil, fmt.Errorf("namespace cannot be empty")
	}
	if podName == "" {
		return nil, fmt.Errorf("pod name cannot be empty")
	}
	if pvcName == "" {
		return nil, fmt.Errorf("PVC name cannot be empty")
	}
	if image == "" {
		image = defaultPodImage
	}
	if mountPath == "" {
		mountPath = "/mnt/storage"
	}

	volumeName := "volume-" + pvcName
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    "consumer-container",
					Image:   image,
					Command: []string{"sleep", "3600"},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      volumeName,
							MountPath: mountPath,
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: volumeName,
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: pvcName,
						},
					},
				},
			},
		},
	}

	log.Debugf("createConsumerPod: namespace=%s name=%s pvcName=%s image=%s mountPath=%s", namespace, podName, pvcName, image, mountPath)

	createdPod, err := kubeClient.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		if errors.IsAlreadyExists(err) {
			log.Debugf("createConsumerPod: pod %q already exists in namespace %q, deleting and recreating", podName, namespace)
			fmt.Printf("Consumer pod %q already exists in namespace %q. Deleting and recreating...\n", podName, namespace)
			if errDel := deletePod(ctx, kubeClient, namespace, podName); errDel != nil {
				return nil, fmt.Errorf("failed to delete existing consumer pod %q: %w", podName, errDel)
			}
			createdPod, err = kubeClient.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
			if err != nil {
				return nil, fmt.Errorf("failed to re-create consumer pod %q in namespace %q: %w", podName, namespace, err)
			}
		} else {
			return nil, fmt.Errorf("failed to create consumer pod %q in namespace %q: %w", podName, namespace, err)
		}
	}

	log.Debugf("createConsumerPod: pod created with UID=%s", createdPod.UID)
	return createdPod, nil
}

// waitForPodScheduledOrRunning polls until the Pod is assigned to a node (Scheduled).
//
// Parameters:
//   - ctx:        Context for cancellation and timeout control
//   - kubeClient: Authenticated Kubernetes clientset
//   - namespace:  Namespace of the pod
//   - podName:    Name of the pod
//
// Returns:
//   - *corev1.Pod: The updated Pod
//   - error: Any error encountered during waiting
func waitForPodScheduled(ctx context.Context, kubeClient kubernetes.Interface, namespace string, podName string) (*corev1.Pod, error) {
	var pod *corev1.Pod
	waitBackoff := createResourceWaitBackoff(ctx)

	log.Debugf("waitForPodScheduled: waiting for pod %q in namespace %q to be scheduled", podName, namespace)

	err := wait.ExponentialBackoffWithContext(ctx, waitBackoff, func(context.Context) (bool, error) {
		current, err := kubeClient.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			log.Debugf("waitForPodScheduled: Get returned error %v", err)
			return false, nil
		}

		log.Debugf("waitForPodScheduled: pod %s node=%s phase=%s", current.Name, current.Spec.NodeName, current.Status.Phase)

		if current.Spec.NodeName != "" {
			pod = current
			return true, nil
		}

		return false, nil
	})

	if err != nil {
		return nil, fmt.Errorf("timed out waiting for pod %q to be scheduled: %w", podName, err)
	}

	return pod, nil
}

// printPodStatusAndEvents prints detailed debugging information about the pod
// including its status, container states, conditions, and associated events.
func printPodStatusAndEvents(ctx context.Context, kubeClient kubernetes.Interface, namespace string, podName string) {
	pod, err := kubeClient.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		fmt.Printf("Error fetching pod %q details: %v\n", podName, err)
		return
	}

	fmt.Printf("\n--- Pod Status Debug Info (%s/%s) ---\n", namespace, podName)
	fmt.Printf("  Phase:       %s\n", pod.Status.Phase)
	fmt.Printf("  Node:        %s\n", pod.Spec.NodeName)
	if pod.Status.Reason != "" {
		fmt.Printf("  Reason:      %s\n", pod.Status.Reason)
	}
	if pod.Status.Message != "" {
		fmt.Printf("  Message:     %s\n", pod.Status.Message)
	}

	if len(pod.Status.Conditions) > 0 {
		fmt.Println("  Conditions:")
		for _, cond := range pod.Status.Conditions {
			fmt.Printf("    - %-25s Status: %-5s Reason: %-15s Message: %s\n", cond.Type, cond.Status, cond.Reason, cond.Message)
		}
	}

	if len(pod.Status.ContainerStatuses) > 0 {
		fmt.Println("  Container Statuses:")
		for _, cs := range pod.Status.ContainerStatuses {
			fmt.Printf("    - Container %q: Ready=%t, RestartCount=%d\n", cs.Name, cs.Ready, cs.RestartCount)
			if cs.State.Waiting != nil {
				fmt.Printf("        State: Waiting (Reason: %s, Message: %s)\n", cs.State.Waiting.Reason, cs.State.Waiting.Message)
			}
			if cs.State.Running != nil {
				fmt.Printf("        State: Running (StartedAt: %s)\n", cs.State.Running.StartedAt.Time.Format(time.RFC3339))
			}
			if cs.State.Terminated != nil {
				fmt.Printf("        State: Terminated (ExitCode: %d, Reason: %s, Message: %s)\n", cs.State.Terminated.ExitCode, cs.State.Terminated.Reason, cs.State.Terminated.Message)
			}
		}
	}

	// Fetch events involving this pod
	eventList, err := kubeClient.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.name=%s,involvedObject.kind=Pod", podName),
	})
	if err == nil && len(eventList.Items) > 0 {
		fmt.Println("  Pod Events:")
		for _, ev := range eventList.Items {
			fmt.Printf("    - [%s] %s: %s (x%d from %s)\n", ev.Type, ev.Reason, ev.Message, ev.Count, ev.Source.Component)
		}
	}
	fmt.Println("--------------------------------------")
}

// deletePod deletes the Pod with the given name in the specified namespace and waits until it is fully removed.
//
// Parameters:
//   - ctx:        Context for cancellation and timeout control
//   - kubeClient: Authenticated Kubernetes clientset
//   - namespace:  Namespace of the pod
//   - podName:    Name of the pod to delete
//
// Returns:
//   - error: Any error encountered during deletion or the wait
func deletePod(ctx context.Context, kubeClient kubernetes.Interface, namespace string, podName string) error {
	if namespace == "" {
		return fmt.Errorf("namespace cannot be empty")
	}
	if podName == "" {
		return fmt.Errorf("pod name cannot be empty")
	}

	log.Debugf("deletePod: namespace=%s podName=%s", namespace, podName)

	if err := kubeClient.CoreV1().Pods(namespace).Delete(ctx, podName, metav1.DeleteOptions{}); err != nil {
		if errors.IsNotFound(err) {
			log.Debugf("deletePod: Pod %q already not found", podName)
			return nil
		}
		return fmt.Errorf("failed to delete Pod %q in namespace %q: %w", podName, namespace, err)
	}

	log.Debugf("deletePod: delete request accepted, polling until Pod is gone")

	waitBackoff := createResourceWaitBackoff(ctx)

	err := wait.ExponentialBackoffWithContext(ctx, waitBackoff, func(context.Context) (bool, error) {
		_, err2 := kubeClient.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
		if err2 != nil {
			if errors.IsNotFound(err2) {
				return true, nil
			}
			log.Debugf("deletePod: Get returned error %v", err2)
			return false, nil
		}
		return false, nil
	})

	if err != nil {
		return fmt.Errorf("pod %q was not fully deleted: %w", podName, err)
	}

	return nil
}

// deletePVC deletes the OpenShift PersistentVolumeClaim with the given name
// in the specified namespace and waits until it is fully removed.
//
// Parameters:
//   - ctx:        Context for cancellation and timeout control
//   - kubeClient: Authenticated Kubernetes clientset
//   - namespace:  Namespace of the PVC
//   - pvcName:    Name of the PVC to delete
//
// Returns:
//   - error: Any error encountered during deletion or the wait
func deletePVC(ctx context.Context, kubeClient kubernetes.Interface, namespace string, pvcName string) error {
	if namespace == "" {
		return fmt.Errorf("namespace cannot be empty")
	}
	if pvcName == "" {
		return fmt.Errorf("PVC name cannot be empty")
	}

	log.Debugf("deletePVC: namespace=%s pvcName=%s", namespace, pvcName)

	if err := kubeClient.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, pvcName, metav1.DeleteOptions{}); err != nil {
		if errors.IsNotFound(err) {
			log.Debugf("deletePVC: PVC %q already not found", pvcName)
			return nil
		}
		return fmt.Errorf("failed to delete PVC %q in namespace %q: %w", pvcName, namespace, err)
	}

	log.Debugf("deletePVC: delete request accepted, polling until PVC is gone")

	// Poll until the PVC no longer exists (NotFound error from Get)
	waitBackoff := createResourceWaitBackoff(ctx)

	err := wait.ExponentialBackoffWithContext(ctx, waitBackoff, func(context.Context) (bool, error) {
		_, err2 := kubeClient.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, pvcName, metav1.GetOptions{})
		if err2 != nil {
			if errors.IsNotFound(err2) {
				return true, nil
			}
			log.Debugf("deletePVC: Get returned error %v", err2)
			return false, nil
		}
		// PVC still exists, keep waiting
		return false, nil
	})

	if err != nil {
		return fmt.Errorf("PVC %q was not fully deleted: %w", pvcName, err)
	}

	return nil
}

// promptContinue formats and prints a prompt to stdout and reads a line from stdin.
// Returns true only when the user types "yes" (case-insensitive).
// Returns false on EOF or I/O error.
func promptContinue(format string, args ...any) bool {
	fmt.Printf(format+" [yes/no]: ", args...)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return strings.EqualFold(strings.TrimSpace(scanner.Text()), "yes")
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to read stdin: %v\n", err)
	}
	return false
}

func main() {
	ptrKubeconfig := flag.String("kubeconfig", "", "Path to kubeconfig file (optional, defaults to $KUBECONFIG or ~/.kube/config)")
	ptrNamespace := flag.String("namespace", defaultNamespace, "Namespace/project to create resources in")
	ptrName := flag.String("name", "", "Name of the PersistentVolumeClaim to create")
	ptrSize := flag.Int("size", defaultVolumeSize, "Volume size in GiB")
	ptrStorageClass := flag.String("storageClass", "", "StorageClass name (optional, uses cluster default if omitted)")
	ptrAccessMode := flag.String("accessMode", string(corev1.ReadWriteOnce), "Access mode (ReadWriteOnce, ReadOnlyMany, ReadWriteMany, ReadWriteOncePod)")
	ptrCreateConsumerPod := flag.String("createConsumerPod", "true", "Create a consumer Pod mounting the PVC to trigger volume binding (true/false)")
	ptrPodName := flag.String("podName", "", "Name of the consumer Pod (optional, defaults to '<name>-consumer-pod')")
	ptrPodImage := flag.String("podImage", defaultPodImage, "Container image for the consumer pod")
	ptrMountPath := flag.String("mountPath", "/mnt/storage", "Mount path for the volume inside consumer pod")
	ptrShouldDebug := flag.String("shouldDebug", "false", "Enable debug output (true/false)")

	flag.Parse()

	shouldDebug, err := parseBoolFlag(*ptrShouldDebug, "shouldDebug")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	createPod, err := parseBoolFlag(*ptrCreateConsumerPod, "createConsumerPod")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	log = initLogger(shouldDebug)
	if shouldDebug {
		log.Debugf("Debug mode enabled")
	}

	if *ptrName == "" {
		fmt.Println("Error: --name is required")
		flag.Usage()
		os.Exit(1)
	}

	podName := *ptrPodName
	if podName == "" {
		podName = *ptrName + "-consumer-pod"
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	log.Debugf("kubeconfig=%s namespace=%s name=%s size=%d storageClass=%q accessMode=%s createConsumerPod=%t podName=%s",
		*ptrKubeconfig, *ptrNamespace, *ptrName, *ptrSize, *ptrStorageClass, *ptrAccessMode, createPod, podName)

	// Obtain Kubernetes/OpenShift client
	kubeClient, err := getKubernetesClient(ctx, *ptrKubeconfig)
	if err != nil {
		fmt.Printf("failed to get Kubernetes client: %v\n", err)
		os.Exit(1)
	}

	pvc, err := createPVC(ctx, kubeClient, *ptrNamespace, *ptrName, *ptrSize, *ptrStorageClass, corev1.PersistentVolumeAccessMode(*ptrAccessMode))
	if err != nil {
		fmt.Printf("failed to createPVC: %v\n", err)
		os.Exit(1)
	}

	storageClassName := ""
	if pvc.Spec.StorageClassName != nil {
		storageClassName = *pvc.Spec.StorageClassName
	}

	storageReq := ""
	if req, ok := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
		storageReq = req.String()
	}

	fmt.Printf("PersistentVolumeClaim created successfully:\n")
	fmt.Printf("  UID:           %s\n", pvc.UID)
	fmt.Printf("  Namespace:     %s\n", pvc.Namespace)
	fmt.Printf("  Name:          %s\n", pvc.Name)
	fmt.Printf("  Phase:         %s\n", pvc.Status.Phase)
	fmt.Printf("  Volume:        %s\n", pvc.Spec.VolumeName)
	fmt.Printf("  StorageClass:  %s\n", storageClassName)
	fmt.Printf("  Size Requested:%s\n", storageReq)
	fmt.Printf("  AccessModes:   %v\n", pvc.Spec.AccessModes)

	var createdConsumerPod *corev1.Pod
	if createPod {
		fmt.Printf("\nCreating consumer pod %q to trigger volume provisioning and binding...\n", podName)
		createdConsumerPod, err = createConsumerPod(ctx, kubeClient, *ptrNamespace, podName, pvc.Name, *ptrPodImage, *ptrMountPath)
		if err != nil {
			fmt.Printf("failed to createConsumerPod: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Consumer pod %q created (UID: %s). Waiting for scheduling and PVC binding...\n", createdConsumerPod.Name, createdConsumerPod.UID)

		scheduledPod, err := waitForPodScheduled(ctx, kubeClient, *ptrNamespace, podName)
		if err != nil {
			fmt.Printf("warning: %v\n", err)
		} else {
			fmt.Printf("Consumer pod scheduled to node: %s\n", scheduledPod.Spec.NodeName)
		}

		boundPVC, err := waitForPVCBound(ctx, kubeClient, *ptrNamespace, pvc.Name)
		if err != nil {
			fmt.Printf("warning: %v\n", err)
		} else {
			pvc = boundPVC
			fmt.Printf("PersistentVolumeClaim bound successfully to volume: %s\n", pvc.Spec.VolumeName)
		}

		printPodStatusAndEvents(ctx, kubeClient, *ptrNamespace, podName)
	}

	if createdConsumerPod != nil {
		if promptContinue("\nDelete consumer pod %q in namespace %q?", createdConsumerPod.Name, createdConsumerPod.Namespace) {
			if err := deletePod(ctx, kubeClient, createdConsumerPod.Namespace, createdConsumerPod.Name); err != nil {
				fmt.Printf("failed to deletePod: %v\n", err)
			} else {
				fmt.Printf("Consumer pod %q deleted successfully.\n", createdConsumerPod.Name)
			}
		} else {
			fmt.Println("Skipping pod deletion.")
		}
	}

	if !promptContinue("\nDelete PersistentVolumeClaim %q in namespace %q (UID: %s)?", pvc.Name, pvc.Namespace, pvc.UID) {
		fmt.Println("Skipping deletion.")
		return
	}

	if err := deletePVC(ctx, kubeClient, pvc.Namespace, pvc.Name); err != nil {
		fmt.Printf("failed to deletePVC: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("PersistentVolumeClaim %q in namespace %q deleted successfully.\n", pvc.Name, pvc.Namespace)
}
