package main

import (
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/recorder"
)

type recorderProvider struct {
	// scheme to specify when creating a recorder
	scheme *runtime.Scheme
	// eventBroadcaster to create new recorder instance
	eventBroadcaster record.EventBroadcaster
	// logger is the logger to use when logging diagnostic event info
	logger logr.Logger
}

func NewRecorderProvider(config *rest.Config, scheme *runtime.Scheme, logger logr.Logger) (recorder.Provider, error) {
	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to init clientSet: %v", err)
	}

	p := &recorderProvider{scheme: scheme, logger: logger}
	p.eventBroadcaster = record.NewBroadcaster()
	p.eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: clientSet.CoreV1().Events("")})
	p.eventBroadcaster.StartEventWatcher(
		func(e *corev1.Event) {
			p.logger.V(1).Info(e.Type, "object", e.InvolvedObject, "reason", e.Reason, "message", e.Message)
		})

	return p, nil
}

func (p *recorderProvider) GetEventRecorderFor(name string) record.EventRecorder {
	return p.eventBroadcaster.NewRecorder(p.scheme, corev1.EventSource{Component: name})
}
