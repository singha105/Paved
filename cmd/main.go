/*
Copyright 2026 Arnab Singh.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	rolloutsv1alpha1 "github.com/argoproj/argo-rollouts/pkg/apis/rollouts/v1alpha1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
	"github.com/singha105/paved/internal/builders"
	"github.com/singha105/paved/internal/controller"
	"github.com/singha105/paved/internal/slo"
	webhookv1alpha1 "github.com/singha105/paved/internal/webhook/v1alpha1"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

// addToScheme registers every API type the manager reads or writes. Errors are returned
// rather than panicking, so a failure is logged and the process exits cleanly.
func addToScheme(s *runtime.Scheme) error {
	for _, add := range []func(*runtime.Scheme) error{
		clientgoscheme.AddToScheme,
		platformv1alpha1.AddToScheme,
		rolloutsv1alpha1.AddToScheme,
		monitoringv1.AddToScheme,
	} {
		if err := add(s); err != nil {
			return err
		}
	}
	// +kubebuilder:scaffold:scheme
	return nil
}

// managedObjectCache limits the manager's cache for every managed kind to objects the
// platform created. Without it the controller would hold every Namespace, ConfigMap and
// Service in the cluster in memory. ServiceClaims are cached unfiltered. With storage, the ACK
// Roles and Buckets are managed kinds too.
func managedObjectCache(storage bool) cache.Options {
	managed := labels.SelectorFromSet(labels.Set{builders.LabelManagedBy: builders.ManagedBy})
	kinds := builders.ManagedTypes()
	if storage {
		kinds = append(kinds, builders.StorageTypes()...)
	}
	byObject := make(map[client.Object]cache.ByObject, len(kinds))
	for _, obj := range kinds {
		byObject[obj] = cache.ByObject{Label: managed}
	}
	return cache.Options{ByObject: byObject}
}

// storageForCluster returns the AWS account claims with storage use, or nil when the cluster isn't
// linked to AWS: there are no AWS settings in the environment, or no ACK CRDs to write a Role and
// Bucket to (DECISIONS.md, ADR-025). Settings that are present but wrong, or an API server that
// can't be asked, are errors.
func storageForCluster(restConfig *rest.Config) (*builders.StorageConfig, error) {
	storage, err := controller.StorageConfigFromEnv(os.Getenv)
	if err != nil {
		return nil, err
	}
	if storage == nil {
		setupLog.Info("Storage is off: no AWS settings in the environment")
		return nil, nil
	}
	c, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("creating a client to look for the ACK CRDs: %w", err)
	}
	if err := controller.ServesStorageKinds(c.RESTMapper()); err != nil {
		if !meta.IsNoMatchError(err) {
			return nil, err
		}
		setupLog.Info("Storage is off: the ACK CRDs are not installed", "reason", err.Error())
		return nil, nil
	}
	setupLog.Info("Storage is on", "region", storage.Region, "issuer", storage.Issuer)
	return storage, nil
}

// nolint:gocyclo
func main() {
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var webhookPort int
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var prometheusURL string
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.IntVar(&webhookPort, "webhook-port", 9443, "Port the webhook server listens on. "+
		"Defaults to 9443. Set -1 to disable the webhook server.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&prometheusURL, "prometheus-url", slo.DefaultPrometheusURL,
		"The address of the Prometheus server that SLI error ratios are read from.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	if err := addToScheme(scheme); err != nil {
		setupLog.Error(err, "Failed to register API types")
		os.Exit(1)
	}

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("Disabling HTTP/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Initial webhook TLS options
	webhookTLSOpts := tlsOpts
	webhookServerOptions := webhook.Options{
		TLSOpts: webhookTLSOpts,
		Port:    webhookPort,
	}

	if len(webhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		webhookServerOptions.CertDir = webhookCertPath
		webhookServerOptions.CertName = webhookCertName
		webhookServerOptions.KeyName = webhookCertKey
	}

	webhookServer := webhook.NewServer(webhookServerOptions)

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.25.0/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.25.0/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	//
	// TODO(user): If you enable certManager, uncomment the following lines:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml to generate and use certificates
	// managed by cert-manager for the metrics server.
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml for TLS certification.
	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		metricsServerOptions.CertDir = metricsCertPath
		metricsServerOptions.CertName = metricsCertName
		metricsServerOptions.KeyName = metricsCertKey
	}

	restConfig := ctrl.GetConfigOrDie()
	storage, err := storageForCluster(restConfig)
	if err != nil {
		setupLog.Error(err, "Failed to read the AWS storage settings")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:                 scheme,
		Cache:                  managedObjectCache(storage != nil),
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "4ff88f17.paved.dev",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "Failed to start manager")
		os.Exit(1)
	}

	prometheus, err := slo.NewPrometheusClient(prometheusURL)
	if err != nil {
		setupLog.Error(err, "Failed to create Prometheus client", "url", prometheusURL)
		os.Exit(1)
	}
	setupLog.Info("Reading SLOs from Prometheus", "url", prometheusURL)

	if err := (&controller.ServiceClaimReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		Prometheus: prometheus,
		APIReader:  mgr.GetAPIReader(),
		Recorder:   mgr.GetEventRecorder("paved-controller"),
		Storage:    storage,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "serviceclaim")
		os.Exit(1)
	}
	// nolint:goconst
	if os.Getenv("ENABLE_WEBHOOKS") != "false" {
		if err := webhookv1alpha1.SetupServiceClaimWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "Failed to create webhook", "webhook", "ServiceClaim")
			os.Exit(1)
		}
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("Starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "Failed to run manager")
		os.Exit(1)
	}
}
