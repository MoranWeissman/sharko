package events

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
)

// EventRecorder is a wrapper around k8s.io/client-go/tools/record.EventRecorder
// that provides a stable interface for emitting Kubernetes events and is nil-safe
// when not in-cluster (local/dev mode).
//
// Use the NewRecorder constructor to create an instance. When not in-cluster,
// NewRecorder returns a nil recorder, and all methods on the wrapper are no-ops.
type EventRecorder struct {
	recorder record.EventRecorder
	// namespace where Sharko is running (for reference objects)
	namespace string
}

// Component name used as the event source for all Sharko-emitted events.
const ComponentName = "sharko"

// NewRecorder creates an EventRecorder that emits events to the sharko namespace.
// Pass the in-cluster k8s clientset and the namespace where Sharko is running.
//
// If clientset is nil (e.g., not running in-cluster), returns a nil-safe no-op recorder.
func NewRecorder(clientset kubernetes.Interface, namespace string) *EventRecorder {
	if clientset == nil {
		return &EventRecorder{recorder: nil, namespace: namespace}
	}

	// Create event broadcaster and start sending events to the API server
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{
		Interface: clientset.CoreV1().Events(namespace),
	})

	// Create recorder with Sharko component identity
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{
		Component: ComponentName,
	})

	return &EventRecorder{
		recorder:  recorder,
		namespace: namespace,
	}
}

// Event emits a normal or warning event. Reason should be a stable UpperCamelCase
// constant from reasons.go. Message is human-readable plain English.
//
// SECURITY: Never include secret material (tokens, kubeconfigs, credentials,
// secret values) in the message. Events are visible cluster-wide.
//
// If the recorder is nil (not in-cluster), this is a no-op.
func (r *EventRecorder) Event(reason, message string, eventType EventType) {
	if r == nil || r.recorder == nil {
		return
	}

	// Use a Pod reference in the Sharko namespace as the event subject.
	// We use a generic "sharko-server" object name since Sharko may not be running
	// as a Deployment with predictable pod names, and we want a stable reference.
	ref := &corev1.ObjectReference{
		Kind:      "Pod",
		Name:      "sharko-server",
		Namespace: r.namespace,
	}

	r.recorder.Event(ref, string(eventType), reason, message)
}

// Eventf is like Event but with fmt.Sprintf formatting.
func (r *EventRecorder) Eventf(reason, messageFmt string, eventType EventType, args ...interface{}) {
	if r == nil || r.recorder == nil {
		return
	}

	ref := &corev1.ObjectReference{
		Kind:      "Pod",
		Name:      "sharko-server",
		Namespace: r.namespace,
	}

	r.recorder.Eventf(ref, string(eventType), reason, messageFmt, args...)
}

// EventType is Normal or Warning.
type EventType string

const (
	// EventTypeNormal indicates a normal operational event (success, milestone).
	EventTypeNormal EventType = "Normal"
	// EventTypeWarning indicates a failure or degraded state.
	EventTypeWarning EventType = "Warning"
)

// AnnotatedEventf emits an event with annotations. Use sparingly — most events
// don't need annotations.
func (r *EventRecorder) AnnotatedEventf(annotations map[string]string, reason, messageFmt string, eventType EventType, args ...interface{}) {
	if r == nil || r.recorder == nil {
		return
	}

	ref := &corev1.ObjectReference{
		Kind:      "Pod",
		Name:      "sharko-server",
		Namespace: r.namespace,
	}

	r.recorder.AnnotatedEventf(ref, annotations, string(eventType), reason, messageFmt, args...)
}

// IsNil returns true if the recorder is nil (not in-cluster / disabled).
func (r *EventRecorder) IsNil() bool {
	return r == nil || r.recorder == nil
}

// WithContext is a convenience to pass the recorder through a context.
// Not currently used but provided for future subsystems that need it.
type contextKey int

const recorderKey contextKey = 0

// NewContext returns a context with the recorder attached.
func NewContext(ctx context.Context, recorder *EventRecorder) context.Context {
	return context.WithValue(ctx, recorderKey, recorder)
}

// FromContext extracts the recorder from the context, or returns nil if not present.
func FromContext(ctx context.Context) *EventRecorder {
	if rec, ok := ctx.Value(recorderKey).(*EventRecorder); ok {
		return rec
	}
	return nil
}
