package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kausality-io/kausality/cmd/kausality-cli/pkg/cli"
)

func main() {
	var (
		kubeconfig string
		namespace  string
		group      string
		version    string
		kind       string
	)

	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file (default: $KUBECONFIG or ~/.kube/config)")
	flag.StringVar(&namespace, "namespace", "", "Namespace to watch (default: all namespaces)")
	flag.StringVar(&group, "group", "", "API group of resources to monitor")
	flag.StringVar(&version, "version", "v1", "API version of resources to monitor")
	flag.StringVar(&kind, "kind", "", "Kind of resources to monitor (required)")
	flag.Parse()

	if kind == "" {
		fmt.Fprintln(os.Stderr, "Error: --kind is required")
		flag.Usage()
		os.Exit(1)
	}

	// Build kubeconfig
	if kubeconfig == "" {
		kubeconfig = os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			home, _ := os.UserHomeDir()
			kubeconfig = home + "/.kube/config"
		}
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error building kubeconfig: %v\n", err)
		os.Exit(1)
	}

	// Create client
	k8sClient, err := client.New(config, client.Options{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating client: %v\n", err)
		os.Exit(1)
	}

	// Create CLI client
	cliClient := cli.NewClient(k8sClient, namespace)

	// Build GVK
	gvk := schema.GroupVersionKind{
		Group:   group,
		Version: version,
		Kind:    kind + "List",
	}

	// Load initial items
	items, err := cliClient.ListDrifts(context.Background(), gvk)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing drifts: %v\n", err)
		os.Exit(1)
	}

	// Create model
	model := cli.NewModel(cliClient)
	model.SetItems(items)

	// Run TUI
	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
}
