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

// Package test runs the paved controller the way the manager runs it, against a real API server
// (envtest), and checks what a claim turns into from the outside: the objects in the API, not the
// reconciler's return values.
package test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	rolloutsv1alpha1 "github.com/argoproj/argo-rollouts/pkg/apis/rollouts/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
	"github.com/singha105/paved/internal/controller"
	"github.com/singha105/paved/internal/slo"
)

var (
	ctx        context.Context
	cancel     context.CancelFunc
	testEnv    *envtest.Environment
	k8sClient  client.Client
	reconciler *controller.ServiceClaimReconciler
)

func TestPaved(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "paved envtest suite")
}

// noTraffic answers every SLO query the way Prometheus does for a service with no requests yet.
type noTraffic struct{}

func (noTraffic) QueryValue(context.Context, string) (float64, error) {
	return 0, slo.ErrNoData
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	ctx, cancel = context.WithCancel(context.Background())

	scheme := runtime.NewScheme()
	for _, addToScheme := range []func(*runtime.Scheme) error{
		clientgoscheme.AddToScheme,
		platformv1alpha1.AddToScheme,
		rolloutsv1alpha1.AddToScheme,
		monitoringv1.AddToScheme,
	} {
		Expect(addToScheme(scheme)).To(Succeed())
	}

	By("starting an API server with the ServiceClaim CRD and the third-party CRDs paved writes")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases"), "crds"},
		ErrorIfCRDPathMissing: true,
	}
	if dir := envtestBinaryDir(); dir != "" {
		testEnv.BinaryAssetsDirectory = dir
	}
	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	By("running the ServiceClaim controller in a manager, as cmd/main.go does")
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	Expect(err).NotTo(HaveOccurred())
	reconciler = &controller.ServiceClaimReconciler{
		Client:     mgr.GetClient(),
		Scheme:     scheme,
		Prometheus: noTraffic{},
		APIReader:  mgr.GetAPIReader(),
		Recorder:   mgr.GetEventRecorder("paved-controller"),
	}
	Expect(reconciler.SetupWithManager(mgr)).To(Succeed())

	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(ctx)).To(Succeed())
	}()
})

var _ = AfterSuite(func() {
	By("stopping the manager and the API server")
	cancel()
	Expect(testEnv.Stop()).To(Succeed())
})

// envtestBinaryDir returns the envtest binaries `make setup-envtest` downloaded, so the suite also
// runs outside `make test`, for example from an IDE. It returns "" when there are none.
func envtestBinaryDir() string {
	base := filepath.Join("..", "bin", "k8s")
	entries, err := os.ReadDir(base)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(base, entry.Name())
		}
	}
	return ""
}
