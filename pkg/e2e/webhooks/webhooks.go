package webhooks

import (
	"context"
	"fmt"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openshift/osde2e/pkg/common/alert"
	"github.com/openshift/osde2e/pkg/common/helper"
	"github.com/openshift/osde2e/pkg/common/labels"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
)

const (
	deletePodWaitDuration = 5 * time.Minute
	createPodWaitDuration = 1 * time.Minute

	suiteName     = "Managed Cluster Validating Webhooks"
	namespaceName = "openshift-validation-webhook"
	serviceName   = "validation-webhook"
	daemonsetName = "validation-webhook"
	configMapName = "webhook-cert"
	secretName    = "webhook-cert"
)

func init() {
	alert.RegisterGinkgoAlert(suiteName, "SD-SREP", "", "sd-cicd-alerts", "sd-cicd@redhat.com", 4)
}

var h *helper.H

var _ = ginkgo.BeforeSuite(func() {
	h = helper.New()
})

var _ = Describe(suiteName, ginkgo.Ordered, func() {
	ginkgo.It("exists and is running", func(ctx context.Context) {
		client := asUser(h, "")

		ginkgo.By("checking the namespace exists")
		err := client.Get(ctx, namespaceName, namespaceName, &v1.Namespace{})
		Expect(err).ShouldNot(HaveOccurred(), "project should have been created")

		ginkgo.By("checking the configmaps exist")
		err = client.Get(ctx, configMapName, namespaceName, &v1.ConfigMap{})
		Expect(err).ShouldNot(HaveOccurred(), "failed to get config map %s", configMapName)

		ginkgo.By("checking the secret exists")
		err = client.Get(ctx, secretName, namespaceName, &v1.Secret{})
		Expect(err).ShouldNot(HaveOccurred(), "failed to get secret %s", secretName)

		ginkgo.By("checking the service exists")
		err = client.Get(ctx, serviceName, namespaceName, &v1.Service{})
		Expect(err).ShouldNot(HaveOccurred(), "no Service named %s found.", serviceName)

		ginkgo.By("checking the daemonset exists")
		err = waitForDaemonSetAvailable(client, daemonsetName, namespaceName)
		Expect(err).ShouldNot(HaveOccurred(), "no DaemonSet named %s found.", daemonsetName)
	})

	ginkgo.Describe("created pods scheduled onto master and infra nodes", func() {
		const privilegedNamespace = "openshift-backplane"
		const unprivilegedNamespace = "openshift-logging"
		var pod *v1.Pod

		ginkgo.BeforeEach(func() {
			name := envconf.RandomName("osde2e", 12)
			pod = newTestPod(name)
		})

		ginkgo.AfterEach(func(ctx context.Context) {
			err := asUser(h, "").Delete(ctx, pod)
			if !apierrors.IsNotFound(err) {
				Expect(err).ShouldNot(HaveOccurred(), "failed to delete test pod")
			}
		})

		ginkgo.It("are blocked", func(ctx context.Context) {
			ginkgo.By("impersonating dedicated-admin and using a privileged namespace")
			pod = withNamespace(pod, privilegedNamespace)
			err := asDedicatedAdmin(h).Create(ctx, pod)
			Expect(apierrors.IsForbidden(err)).To(BeTrue(), "expected forbidden error", err)

			ginkgo.By("impersonating a random user and using a privileged namespace")
			client := asUser(h, "majora")
			err = client.Create(ctx, pod)
			Expect(apierrors.IsForbidden(err)).To(BeTrue(), "expected forbidden error", err)

			ginkgo.By("impersonating a random user and using an unprivileged namespace")
			err = client.Create(ctx, withNamespace(pod, unprivilegedNamespace))
			Expect(apierrors.IsForbidden(err)).To(BeTrue(), "expected forbidden error", err)
		}, ginkgo.SpecTimeout(createPodWaitDuration.Seconds()+deletePodWaitDuration.Seconds()))

		ginkgo.It("are allowed", func(ctx context.Context) {
			ginkgo.By("impersonating dedicated-admin-project ServiceAccount")
			client := asServiceAccount(h, fmt.Sprintf("system:serviceaccount:%s:dedicated-admin-project", h.CurrentProject()))
			err := client.Create(ctx, withNamespace(pod, privilegedNamespace))
			Expect(err).ShouldNot(HaveOccurred(), "failed to create pod")
		}, ginkgo.SpecTimeout(createPodWaitDuration.Seconds()+deletePodWaitDuration.Seconds()))
	})
})

func Describe(name string, args ...any) bool {
	return ginkgo.Describe(name, labels.OSD, labels.ROSA, labels.STS, args)
}

// move these helpers somewhere

func newTestPod(name string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "test",
					Image: "registry.access.redhat.com/ubi8/ubi-minimal",
				},
			},
			Tolerations: []v1.Toleration{
				{
					Key:    "node-role.kubernetes.io/master",
					Value:  "toleration-key-value",
					Effect: v1.TaintEffectNoSchedule,
				}, {
					Key:    "node-role.kubernetes.io/infra",
					Value:  "toleration-key-value2",
					Effect: v1.TaintEffectNoSchedule,
				},
			},
		},
	}
}

func withNamespace(pod *v1.Pod, namespace string) *v1.Pod {
	pod.ObjectMeta.Namespace = namespace
	return pod
}

func asServiceAccount(h *helper.H, sa string) *resources.Resources {
	h.ServiceAccount = sa
	return asUser(h, sa)
}

func asUser(h *helper.H, user string, groups ...string) *resources.Resources {
	// these groups are required for impersonating a user
	if user != "" {
		groups = append(groups, "system:authenticated", "system:authenticated:oauth")
	}

	h.Impersonate(rest.ImpersonationConfig{
		UserName: user,
		Groups:   groups,
	})

	client, err := resources.New(h.GetConfig())
	Expect(err).NotTo(HaveOccurred(), "failed to create resources client object")

	return client
}

func asDedicatedAdmin(h *helper.H) *resources.Resources {
	return asUser(h, "test-user@redhat.com", "dedicated-admins")
}

func waitForDaemonSetAvailable(resources *resources.Resources, name string, namespace string) error {
	ds := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	return wait.For(conditions.New(resources).ResourceMatch(ds, func(object k8s.Object) bool {
		d := object.(*appsv1.DaemonSet)
		desiredNumScheduled := d.Status.DesiredNumberScheduled

		return d.Status.CurrentNumberScheduled == desiredNumScheduled &&
			d.Status.NumberReady == desiredNumScheduled &&
			d.Status.NumberAvailable == desiredNumScheduled
	}))
}
