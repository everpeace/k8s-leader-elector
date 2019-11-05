package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"

	"k8s.io/klog"
	"k8s.io/klog/klogr"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/runtime/signals"

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
	var initialWait bool
	var versionFlag bool

	klog.InitFlags(nil)
	flag.StringVar(&name, "name", "k8s-leader-elector", "leader election name")
	flag.StringVar(&namespace, "namespace", "", "leader election name")
	flag.DurationVar(&leaseDuration, "lease-duration", 10*time.Second, "lease duration of leader lease")
	flag.DurationVar(&renewDeadline, "renew-deadline", 5*time.Second, "deadline for renewing leader lease")
	flag.DurationVar(&retryPeriod, "retry-period", 1*time.Second, "retry period for acquiring leader lease")
	flag.BoolVar(&initialWait, "initial-wait", false, "wait for the old lease being expired if no leader exist.")
	flag.BoolVar(&versionFlag, "version", false, "display version and exit")
	flag.Parse()
	taskCmd := flag.Args()
	logf.SetLogger(klogr.New())
	log := logf.Log.WithName("main")

	if versionFlag {
		fmt.Printf("k8s-leader-elector version=%s revision=%s\n", Version, Revision)
		os.Exit(0)
	}

	log.Info("k8s-leader-elector", "version", Version, "revision", Revision)
	log.V(2).Info("configured options",
		"name", name,
		"namespace", namespace,
		"lease-duration", leaseDuration.String(),
		"renew-deadline", renewDeadline.String(),
		"retry-period", retryPeriod.String(),
		"initial-wait", initialWait,
	)
	clientConfig := config.GetConfigOrDie()

	elector, err := NewSingleRoundLeaderElector(
		clientConfig, log.WithName("leader-elector"),
		name, namespace,
		taskCmd,
		leaseDuration, renewDeadline, retryPeriod,
		initialWait,
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
