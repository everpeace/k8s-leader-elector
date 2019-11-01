package main

import (
	"flag"
	"k8s.io/klog"
	"k8s.io/klog/klogr"
	"os"
	"os/exec"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/runtime/signals"
	"time"

	_ "k8s.io/client-go/plugin/pkg/client/auth" // import all the auth plugins
)

var (
	// LDFLAGS should overwrite these variables in build time.
	Version  string
	Revision string
)

func main() {
	var name string
	var namespace string
	var leaseDuration time.Duration
	var renewDeadline time.Duration
	var retryPeriod time.Duration
	klog.InitFlags(nil)
	flag.StringVar(&name, "name", "k8s-leader-elector", "leader election name")
	flag.StringVar(&namespace, "namespace", "", "leader election name")
	flag.DurationVar(&leaseDuration, "lease-duration", 10*time.Second, "lease duration of leader lease")
	flag.DurationVar(&renewDeadline, "renew-deadline", 5*time.Second, "deadline for renewing leader lease")
	flag.DurationVar(&retryPeriod, "retry-period", 1*time.Second, "retry period for acquiring leader lease")
	flag.Parse()
	taskCmd := flag.Args()
	logf.SetLogger(klogr.New())
	log := logf.Log.WithName("main")

	log.Info("k8s-leader-elector", "Version", Version, "Revision", Revision)

	config := config.GetConfigOrDie()

	elector, err := NewSingleRoundLeaderElector(
		config, log.WithName("leader-elector"),
		name, namespace,
		taskCmd,
		leaseDuration, renewDeadline, retryPeriod,
	)
	if err != nil {
		log.Error(err, "can't create leader-elector")
		os.Exit(1)
	}

	if err := elector.Start(signals.SetupSignalHandler()); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			klog.Flush()
			log.Info("exiting", "code", ee.ProcessState.ExitCode())
			os.Exit(ee.ProcessState.ExitCode())
		}
		log.Error(err, "error in leader-elector")
		klog.Flush()
		os.Exit(1)
	}
}
