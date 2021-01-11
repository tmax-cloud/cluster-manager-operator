/*


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

package controllers

import (
	"context"
	"encoding/json"

	"github.com/go-logr/logr"
	servicecatalogv1beta1 "github.com/kubernetes-sigs/service-catalog/pkg/apis/servicecatalog/v1beta1"
	"github.com/prometheus/common/log"
	clusterv1alpha1 "github.com/tmax-cloud/cluster-manager-operator/api/v1alpha1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"
	controlplanev1 "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1alpha3"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	CAPI_SYSTEM_NAMESPACE       = "capi-system"
	CLAIM_API_GROUP             = "claims.tmax.io"
	CLUSTER_API_GROUP           = "cluster.tmax.io"
	CLAIM_API_Kind              = "clusterclaims"
	CLAIM_API_GROUP_VERSION     = "claims.tmax.io/v1alpha1"
	HCR_API_GROUP_VERSION       = "multi.hyper.tmax.io/v1"
	HYPERCLOUD_SYSTEM_NAMESPACE = "hypercloud5-system"
	HCR_SYSTEM_NAMESPACE        = "kube-federation-system"
)

type ClusterParameter struct {
	ClusterName string
	AWSRegion   string
	SshKey      string
	MasterNum   int
	MasterType  string
	WorkerNum   int
	WorkerType  string
	Owner       string
}

// ClusterManagerReconciler reconciles a ClusterManager object
type ClusterManagerReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=cluster.tmax.io,resources=clustermanagers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cluster.tmax.io,resources=clustermanagers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machinedeployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machinedeployments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=controlplane.cluster.x-k8s.io,resources=kubeadmcontrolplanes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=controlplane.cluster.x-k8s.io,resources=kubeadmcontrolplanes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=servicecatalog.k8s.io,resources=serviceinstances,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=servicecatalog.k8s.io,resources=serviceinstances/status,verbs=get;update;patch

func (r *ClusterManagerReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	_ = context.Background()
	log := r.Log.WithValues("clustermanager", req.NamespacedName)

	//get ClusterManager
	clusterManager := &clusterv1alpha1.ClusterManager{}
	if err := r.Get(context.TODO(), req.NamespacedName, clusterManager); err != nil {
		if errors.IsNotFound(err) {
			log.Info("ClusterManager resource not found. Ignoring since object must be deleted.")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get ClusterManager")
		return ctrl.Result{}, err
	}

	if err := r.CreateServiceInstance(clusterManager); err != nil {
		log.Error(err, "Failed to create ServiceInstance")
		return ctrl.Result{}, err
	}
	if err := r.CreateClusterMnagerOwnerRole(clusterManager); err != nil {
		log.Error(err, "Failed to create ServiceInstance")
		return ctrl.Result{}, err
	}
	// r.CreateMember(clusterManager)

	r.kubeadmControlPlaneUpdate(clusterManager)
	r.machineDeploymentUpdate(clusterManager)

	return ctrl.Result{}, nil
}

///

func (r *ClusterManagerReconciler) CreateServiceInstance(clusterManager *clusterv1alpha1.ClusterManager) error {

	serviceInstance := &servicecatalogv1beta1.ServiceInstance{}
	serviceInstanceKey := types.NamespacedName{Name: clusterManager.Name, Namespace: HYPERCLOUD_SYSTEM_NAMESPACE}
	if err := r.Get(context.TODO(), serviceInstanceKey, serviceInstance); err != nil {
		if errors.IsNotFound(err) {
			clusterParameter := ClusterParameter{
				ClusterName: clusterManager.Name,
				AWSRegion:   clusterManager.Spec.Region,
				SshKey:      clusterManager.Spec.SshKey,
				MasterNum:   clusterManager.Spec.MasterNum,
				MasterType:  clusterManager.Spec.MasterType,
				WorkerNum:   clusterManager.Spec.WorkerNum,
				WorkerType:  clusterManager.Spec.WorkerType,
				Owner:       clusterManager.Annotations["owner"],
			}

			byte, err := json.Marshal(&clusterParameter)
			if err != nil {
				log.Error(err, "Failed to marshal cluster parameters")
				return err
			}

			newServiceInstance := &servicecatalogv1beta1.ServiceInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterManager.Name,
					Namespace: clusterManager.Namespace,
				},
				Spec: servicecatalogv1beta1.ServiceInstanceSpec{
					PlanReference: servicecatalogv1beta1.PlanReference{
						ClusterServiceClassExternalName: "capi-aws-template",
						ClusterServicePlanExternalName:  "capi-aws-template-plan-default",
					},
					Parameters: &runtime.RawExtension{
						Raw: byte,
					},
				},
			}

			ctrl.SetControllerReference(clusterManager, newServiceInstance, r.Scheme)
			err = r.Create(context.TODO(), newServiceInstance)
			if err != nil {
				log.Error(err, "Failed to create "+clusterManager.Name+" serviceInstance")
				return err
			}
		} else {
			log.Error(err, "Failed to get serviceInstance")
			return err
		}
	}
	return nil
}

func (r *ClusterManagerReconciler) CreateClusterMnagerOwnerRole(clusterManager *clusterv1alpha1.ClusterManager) error {
	role := &rbacv1.Role{}
	roleName := clusterManager.Annotations["owner"] + "-" + clusterManager.Name + "-clm-role"
	roleKey := types.NamespacedName{Name: roleName, Namespace: HYPERCLOUD_SYSTEM_NAMESPACE}
	if err := r.Get(context.TODO(), roleKey, role); err != nil {
		if errors.IsNotFound(err) {
			newRole := &rbacv1.Role{
				ObjectMeta: metav1.ObjectMeta{
					Name:      roleName,
					Namespace: HYPERCLOUD_SYSTEM_NAMESPACE,
				},
				Rules: []rbacv1.PolicyRule{
					{APIGroups: []string{CLUSTER_API_GROUP}, Resources: []string{"clustermanagers"},
						ResourceNames: []string{clusterManager.Name}, Verbs: []string{"get", "update", "delete"}},
					{APIGroups: []string{CLUSTER_API_GROUP}, Resources: []string{"clustermanagers/status"},
						ResourceNames: []string{clusterManager.Name}, Verbs: []string{"get"}},
				},
			}
			ctrl.SetControllerReference(clusterManager, newRole, r.Scheme)
			err := r.Create(context.TODO(), newRole)
			if err != nil {
				log.Error(err, "Failed to create "+roleName+" role.")
				return err
			}
		} else {
			log.Error(err, "Failed to get role")
			return err
		}
	}
	roleBinding := &rbacv1.RoleBinding{}
	roleBindingName := clusterManager.Annotations["owner"] + "-" + clusterManager.Name + "-clm-rolebinding"
	roleBindingKey := types.NamespacedName{Name: clusterManager.Name, Namespace: HYPERCLOUD_SYSTEM_NAMESPACE}
	if err := r.Get(context.TODO(), roleBindingKey, roleBinding); err != nil {
		if errors.IsNotFound(err) {
			newRoleBinding := &rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      roleBindingName,
					Namespace: HYPERCLOUD_SYSTEM_NAMESPACE,
				},
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "Role",
					Name:     roleName,
				},
				Subjects: []rbacv1.Subject{
					{
						APIGroup: "rbac.authorization.k8s.io",
						Kind:     "User",
						Name:     clusterManager.Annotations["owner"],
						// Namespace: HCR_SYSTEM_NAMESPACE,
					},
				},
			}
			ctrl.SetControllerReference(clusterManager, newRoleBinding, r.Scheme)
			err = r.Create(context.TODO(), newRoleBinding)
			if err != nil {
				log.Error(err, "Failed to create "+roleBindingName+" role.")
				return err
			}
		} else {
			log.Error(err, "Failed to get rolebinding")
			return err
		}
	}
	return nil
}

func (r *ClusterManagerReconciler) kubeadmControlPlaneUpdate(clusterManager *clusterv1alpha1.ClusterManager) {
	kcp := &controlplanev1.KubeadmControlPlane{}
	key := types.NamespacedName{Name: clusterManager.Name + "-control-plane", Namespace: CAPI_SYSTEM_NAMESPACE}

	if err := r.Get(context.TODO(), key, kcp); err != nil {
		return
	}

	//create helper for patch
	helper, _ := patch.NewHelper(kcp, r.Client)
	defer func() {
		if err := helper.Patch(context.TODO(), kcp); err != nil {
			r.Log.Error(err, "kubeadmcontrolplane patch error")
		}
	}()

	if *kcp.Spec.Replicas != int32(clusterManager.Spec.MasterNum) {
		*kcp.Spec.Replicas = int32(clusterManager.Spec.MasterNum)
	}
}

func (r *ClusterManagerReconciler) machineDeploymentUpdate(clusterManager *clusterv1alpha1.ClusterManager) {
	md := &clusterv1.MachineDeployment{}
	key := types.NamespacedName{Name: clusterManager.Name + "-md-0", Namespace: CAPI_SYSTEM_NAMESPACE}

	if err := r.Get(context.TODO(), key, md); err != nil {
		return
	}

	//create helper for patch
	helper, _ := patch.NewHelper(md, r.Client)
	defer func() {
		if err := helper.Patch(context.TODO(), md); err != nil {
			r.Log.Error(err, "kubeadmcontrolplane patch error")
		}
	}()

	if *md.Spec.Replicas != int32(clusterManager.Spec.WorkerNum) {
		*md.Spec.Replicas = int32(clusterManager.Spec.WorkerNum)
	}
}

func (r *ClusterManagerReconciler) requeueClusterManagersForKubeadmControlPlane(o handler.MapObject) []ctrl.Request {
	cp := o.Object.(*controlplanev1.KubeadmControlPlane)
	log := r.Log.WithValues("objectMapper", "kubeadmControlPlaneToClusterManagers", "namespace", cp.Namespace, "kubeadmcontrolplane", cp.Name)

	// Don't handle deleted kubeadmcontrolplane
	if !cp.ObjectMeta.DeletionTimestamp.IsZero() {
		log.V(4).Info("kubeadmcontrolplane has a deletion timestamp, skipping mapping.")
		return nil
	}

	//get ClusterManager
	clm := &clusterv1alpha1.ClusterManager{}
	key := types.NamespacedName{Namespace: HYPERCLOUD_SYSTEM_NAMESPACE, Name: cp.Name[0 : len(cp.Name)-len("-control-plane")]}
	if err := r.Get(context.TODO(), key, clm); err != nil {
		if errors.IsNotFound(err) {
			log.Info("ClusterManager resource not found. Ignoring since object must be deleted.")
			return nil
		}

		log.Error(err, "Failed to get ClusterManager")
		return nil
	}

	//create helper for patch
	helper, _ := patch.NewHelper(clm, r.Client)
	defer func() {
		if err := helper.Patch(context.TODO(), clm); err != nil {
			log.Error(err, "ClusterManager patch error")
		}
	}()

	clm.Spec.MasterNum = int(*cp.Spec.Replicas)
	clm.Status.MasterRun = int(cp.Status.Replicas)
	clm.Spec.Version = cp.Spec.Version

	return nil
}

func (r *ClusterManagerReconciler) requeueClusterManagersForMachineDeployment(o handler.MapObject) []ctrl.Request {
	md := o.Object.(*clusterv1.MachineDeployment)
	log := r.Log.WithValues("objectMapper", "kubeadmControlPlaneToClusterManagers", "namespace", md.Namespace, "machinedeployment", md.Name)

	// Don't handle deleted machinedeployment
	if !md.ObjectMeta.DeletionTimestamp.IsZero() {
		log.V(4).Info("machinedeployment has a deletion timestamp, skipping mapping.")
		return nil
	}

	//get ClusterManager
	clm := &clusterv1alpha1.ClusterManager{}
	key := types.NamespacedName{Namespace: HYPERCLOUD_SYSTEM_NAMESPACE, Name: md.Name[0 : len(md.Name)-len("-md-0")]}
	if err := r.Get(context.TODO(), key, clm); err != nil {
		if errors.IsNotFound(err) {
			log.Info("ClusterManager resource not found. Ignoring since object must be deleted.")
			return nil
		}

		log.Error(err, "Failed to get ClusterManager")
		return nil
	}

	//create helper for patch
	helper, _ := patch.NewHelper(clm, r.Client)
	defer func() {
		if err := helper.Patch(context.TODO(), clm); err != nil {
			log.Error(err, "ClusterManager patch error")
		}
	}()

	clm.Spec.WorkerNum = int(*md.Spec.Replicas)
	clm.Status.WorkerRun = int(md.Status.Replicas)

	return nil
}

func (r *ClusterManagerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	controller, err := ctrl.NewControllerManagedBy(mgr).
		For(&clusterv1alpha1.ClusterManager{}).
		WithEventFilter(
			predicate.Funcs{
				// Avoid reconciling if the event triggering the reconciliation is related to incremental status updates
				// for kubefedcluster resources only
				CreateFunc: func(e event.CreateEvent) bool {
					return true
				},
				UpdateFunc: func(e event.UpdateEvent) bool {
					oldCLM := e.ObjectOld.(*clusterv1alpha1.ClusterManager).DeepCopy()
					newCLM := e.ObjectNew.(*clusterv1alpha1.ClusterManager).DeepCopy()

					if oldCLM.Spec.MasterNum != newCLM.Spec.MasterNum || oldCLM.Spec.WorkerNum != newCLM.Spec.WorkerNum {
						return true
					}
					return false
				},
				DeleteFunc: func(e event.DeleteEvent) bool {
					return false
				},
				GenericFunc: func(e event.GenericEvent) bool {
					return false
				},
			},
		).
		Build(r)

	if err != nil {
		return err
	}

	controller.Watch(
		&source.Kind{Type: &controlplanev1.KubeadmControlPlane{}},
		&handler.EnqueueRequestsFromMapFunc{
			ToRequests: handler.ToRequestsFunc(r.requeueClusterManagersForKubeadmControlPlane),
		},
		predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				oldKcp := e.ObjectOld.(*controlplanev1.KubeadmControlPlane)
				newKcp := e.ObjectNew.(*controlplanev1.KubeadmControlPlane)

				if *oldKcp.Spec.Replicas != *newKcp.Spec.Replicas || oldKcp.Status.Replicas != newKcp.Status.Replicas || oldKcp.Spec.Version != newKcp.Spec.Version {
					return true
				}
				return false
			},
			CreateFunc: func(e event.CreateEvent) bool {
				return true
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				return false
			},
			GenericFunc: func(e event.GenericEvent) bool {
				return false
			},
		},
	)

	return controller.Watch(
		&source.Kind{Type: &clusterv1.MachineDeployment{}},
		&handler.EnqueueRequestsFromMapFunc{
			ToRequests: handler.ToRequestsFunc(r.requeueClusterManagersForMachineDeployment),
		},
		predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				oldMd := e.ObjectOld.(*clusterv1.MachineDeployment)
				newMd := e.ObjectNew.(*clusterv1.MachineDeployment)

				if *oldMd.Spec.Replicas != *newMd.Spec.Replicas || oldMd.Status.Replicas != newMd.Status.Replicas {
					return true
				}
				return false
			},
			CreateFunc: func(e event.CreateEvent) bool {
				return true
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				return false
			},
			GenericFunc: func(e event.GenericEvent) bool {
				return false
			},
		},
	)
}
