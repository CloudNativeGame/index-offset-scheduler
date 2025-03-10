package main

import (
	"fmt"
	plugin "index-offset-scheduler/pkg"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/cmd/kube-scheduler/app"
	"os"
)

func main() {
	command := app.NewSchedulerCommand(
		app.WithPlugin(plugin.PluginName, plugin.New),
	)

	klog.InitFlags(nil)
	defer klog.Flush()

	if err := command.Execute(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

}
