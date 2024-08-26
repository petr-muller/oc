// Package status displays the status of current cluster version updates.
package status

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	// "sort"
	"strings"
	"time"

	configv1alpha1 "github.com/openshift/api/config/v1alpha1"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	kcmdutil "k8s.io/kubectl/pkg/cmd/util"

	configv1 "github.com/openshift/api/config/v1"
	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"
	routev1 "github.com/openshift/api/route/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	configv1alpha1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1alpha1"
	machineconfigv1client "github.com/openshift/client-go/machineconfiguration/clientset/versioned"
	routev1client "github.com/openshift/client-go/route/clientset/versioned/typed/route/v1"
	"github.com/openshift/oc/pkg/cli/admin/inspectalerts"
	"github.com/openshift/oc/pkg/cli/admin/upgrade/status/mco"
)

func newOptions(streams genericiooptions.IOStreams) *options {
	return &options{
		IOStreams: streams,
	}
}

const (
	detailedOutputNone   = "none"
	detailedOutputAll    = "all"
	detailedOutputNodes  = "nodes"
	detailedOutputHealth = "health"
)

var detailedOutputAllValues = []string{detailedOutputNone, detailedOutputAll, detailedOutputNodes, detailedOutputHealth}

func New(f kcmdutil.Factory, streams genericiooptions.IOStreams) *cobra.Command {
	o := newOptions(streams)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Display the status of current cluster version updates.",
		Run: func(cmd *cobra.Command, args []string) {
			kcmdutil.CheckErr(o.Complete(f, cmd, args))
			kcmdutil.CheckErr(o.Run(cmd.Context()))
		},
	}

	flags := cmd.Flags()
	// TODO: We can remove these flags once the idea about `oc adm upgrade status` stabilizes and the command
	//       is promoted out of the OC_ENABLE_CMD_UPGRADE_STATUS feature gate
	flags.StringVar(&o.mockData.cvPath, "mock-clusterversion", "", "Path to a YAML ClusterVersion object to use for testing (will be removed later). Files in the same directory with the same name and suffixes -co.yaml, -mcp.yaml, -mc.yaml, and -node.yaml are required. Should be only used with --status-api=false.")

	flags.StringVar(&o.detailedOutput, "details", "none", fmt.Sprintf("Show detailed output in selected section. One of: %s", strings.Join(detailedOutputAllValues, ", ")))

	flags.StringVar(&o.mockData.updateStatusPath, "mock-updatestatus", "", "Path to a YAML UpdateStatus object to use for testing. Should only be used with --status-api=true.")
	flags.BoolVar(&o.statusApi, "status-api", false, "Use status API")

	return cmd
}

type options struct {
	genericiooptions.IOStreams

	mockData       mockData
	detailedOutput string

	statusApi bool

	ConfigClient         configv1client.Interface
	ConfigV1Alpha1Client configv1alpha1client.ConfigV1alpha1Interface
	CoreClient           corev1client.CoreV1Interface
	MachineConfigClient  machineconfigv1client.Interface
	RouteClient          routev1client.RouteV1Interface
	getAlerts            func(ctx context.Context) ([]byte, error)
}

func (o *options) enabledDetailed(what string) bool {
	return o.detailedOutput == detailedOutputAll || o.detailedOutput == what
}

func (o *options) Complete(f kcmdutil.Factory, cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return kcmdutil.UsageErrorf(cmd, "positional arguments given")
	}

	if !sets.New[string](detailedOutputAllValues...).Has(o.detailedOutput) {
		return fmt.Errorf("invalid value for --details: %s (must be one of %s)", o.detailedOutput, strings.Join(detailedOutputAllValues, ", "))
	}

	if o.mockData.cvPath != "" {
		if o.statusApi {
			return fmt.Errorf("--mock-clusterversion cannot be used with --status-api=true")
		}

		cvSuffix := "-cv.yaml"
		if o.mockData.cvPath != "" {
			o.mockData.operatorsPath = strings.Replace(o.mockData.cvPath, cvSuffix, "-co.yaml", 1)
			o.mockData.machineConfigPoolsPath = strings.Replace(o.mockData.cvPath, cvSuffix, "-mcp.yaml", 1)
			o.mockData.machineConfigsPath = strings.Replace(o.mockData.cvPath, cvSuffix, "-mc.yaml", 1)
			o.mockData.nodesPath = strings.Replace(o.mockData.cvPath, cvSuffix, "-node.yaml", 1)
			o.mockData.alertsPath = strings.Replace(o.mockData.cvPath, cvSuffix, "-alerts.json", 1)
		}
	}

	if !o.statusApi && o.mockData.updateStatusPath != "" {
		return fmt.Errorf("--mock-updatestatus cannot be used with --status-api=false")
	}

	if o.mockData.cvPath == "" && o.mockData.updateStatusPath == "" {
		cfg, err := f.ToRESTConfig()
		if err != nil {
			return err
		}

		if o.statusApi {
			configv1Alpha1Client := configv1alpha1client.NewForConfigOrDie(cfg)
			o.ConfigV1Alpha1Client = configv1Alpha1Client
		} else {

			configClient, err := configv1client.NewForConfig(cfg)
			if err != nil {
				return err
			}
			o.ConfigClient = configClient

			machineConfigClient, err := machineconfigv1client.NewForConfig(cfg)
			if err != nil {
				return err
			}
			o.MachineConfigClient = machineConfigClient
			coreClient, err := corev1client.NewForConfig(cfg)
			if err != nil {
				return err
			}
			o.CoreClient = coreClient

			routeClient, err := routev1client.NewForConfig(cfg)
			if err != nil {
				return err
			}
			o.RouteClient = routeClient

			routeGetter := func(ctx context.Context, namespace string, name string, opts metav1.GetOptions) (*routev1.Route, error) {
				return routeClient.Routes(namespace).Get(ctx, name, opts)
			}
			o.getAlerts = func(ctx context.Context) ([]byte, error) {
				return inspectalerts.GetAlerts(ctx, routeGetter, cfg.BearerToken)
			}
		}
	} else {
		err := o.mockData.load(o.statusApi)
		if err != nil {
			return err
		}
	}

	return nil
}

func (o *options) Run(ctx context.Context) error {
	var updateInsights []updateInsight

	var cpStatusDisplayData controlPlaneStatusDisplayData
	var cpInsights []updateInsight

	var workerPoolsStatusData []poolDisplayData
	var controlPlanePoolStatusData poolDisplayData
	var isWorkerPoolOutdated bool

	var controlPlaneUpdating bool
	var startedAt time.Time
	var now time.Time

	if !o.statusApi {
		var cv *configv1.ClusterVersion
		now = time.Now()
		if cv = o.mockData.clusterVersion; cv == nil {
			var err error
			cv, err = o.ConfigClient.ConfigV1().ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					return fmt.Errorf("no cluster version information available - you must be connected to an OpenShift version 4 server to fetch the current version")
				}
				return err
			}
		} else {
			// mock "now" to be the latest time when something happened in the mocked data
			// add some nanoseconds to exercise rounding
			now = time.Time{}
			for _, condition := range cv.Status.Conditions {
				if condition.LastTransitionTime.After(now) {
					now = condition.LastTransitionTime.Time.Add(368975 * time.Nanosecond)
				}
			}
		}
		var operators *configv1.ClusterOperatorList
		if operators = o.mockData.clusterOperators; operators == nil {
			var err error
			operators, err = o.ConfigClient.ConfigV1().ClusterOperators().List(ctx, metav1.ListOptions{})
			if err != nil {
				return err
			}
		} else {
			// mock "now" to be the latest time when something happened in the mocked data
			for _, co := range operators.Items {
				for _, condition := range co.Status.Conditions {
					if condition.LastTransitionTime.After(now) {
						now = condition.LastTransitionTime.Time.Add(368975 * time.Nanosecond)
					}
				}
			}
		}
		if len(operators.Items) == 0 {
			return fmt.Errorf("no cluster operator information available - you must be connected to an OpenShift version 4 server")
		}

		var pools *machineconfigv1.MachineConfigPoolList
		if pools = o.mockData.machineConfigPools; pools == nil {
			var err error
			pools, err = o.MachineConfigClient.MachineconfigurationV1().MachineConfigPools().List(ctx, metav1.ListOptions{})
			if err != nil {
				return err
			}
		}
		var allNodes *corev1.NodeList
		if allNodes = o.mockData.nodes; allNodes == nil {
			var err error
			allNodes, err = o.CoreClient.Nodes().List(ctx, metav1.ListOptions{})
			if err != nil {
				return err
			}
		}
		var machineConfigs *machineconfigv1.MachineConfigList
		if machineConfigs = o.mockData.machineConfigs; machineConfigs == nil {
			machineConfigs = &machineconfigv1.MachineConfigList{}
			for _, node := range allNodes.Items {
				for _, key := range []string{mco.CurrentMachineConfigAnnotationKey, mco.DesiredMachineConfigAnnotationKey} {
					machineConfigName, ok := node.Annotations[key]
					if !ok || machineConfigName == "" {
						continue
					}
					mc, err := getMachineConfig(ctx, o.MachineConfigClient, machineConfigs.Items, machineConfigName)
					if err != nil {
						return err
					}
					if mc != nil {
						machineConfigs.Items = append(machineConfigs.Items, *mc)
					}
				}
			}
		}
		var masterSelector labels.Selector
		var workerSelector labels.Selector
		customSelectors := map[string]labels.Selector{}
		for _, pool := range pools.Items {
			s, err := metav1.LabelSelectorAsSelector(pool.Spec.NodeSelector)
			if err != nil {
				return fmt.Errorf("failed to get label selector from the pool: %s", pool.Name)
			}
			switch pool.Name {
			case mco.MachineConfigPoolMaster:
				masterSelector = s
			case mco.MachineConfigPoolWorker:
				workerSelector = s
			default:
				customSelectors[pool.Name] = s
			}
		}

		nodesPerPool := map[string][]corev1.Node{}
		for _, node := range allNodes.Items {
			name := whichPool(masterSelector, workerSelector, customSelectors, node)
			nodesPerPool[name] = append(nodesPerPool[name], node)
		}
		for _, pool := range pools.Items {
			nodesStatusData, insights := assessNodesStatus(cv, pool, nodesPerPool[pool.Name], machineConfigs.Items)
			updateInsights = append(updateInsights, insights...)
			poolStatus, insights := assessMachineConfigPool(pool, nodesStatusData)
			updateInsights = append(updateInsights, insights...)
			if poolStatus.Name == mco.MachineConfigPoolMaster {
				controlPlanePoolStatusData = poolStatus
			} else {
				workerPoolsStatusData = append(workerPoolsStatusData, poolStatus)
			}
		}

		progressing := findClusterOperatorStatusCondition(cv.Status.Conditions, configv1.OperatorProgressing)
		if progressing == nil {
			return fmt.Errorf("no current %s info, see `oc describe clusterversion` for more details.\n", configv1.OperatorProgressing)
		}
		controlPlaneUpdating = progressing.Status == configv1.ConditionTrue
		startedAt = progressing.LastTransitionTime.Time
		if len(cv.Status.History) > 0 {
			startedAt = cv.Status.History[0].StartedTime.Time
		}
		// get the alerts for the cluster. if we're unable to fetch the alerts, we'll let the user know that alerts
		// are not being fetched, but rest of the command should work.
		var alertData AlertData
		var alertBytes []byte
		var err error
		if ap := o.mockData.alertsPath; ap != "" {
			alertBytes, err = os.ReadFile(o.mockData.alertsPath)
		} else {
			alertBytes, err = o.getAlerts(ctx)
		}
		if err != nil {
			fmt.Println("Unable to fetch alerts, ignoring alerts in 'Update Health': ", err)
		} else {
			// Unmarshal the JSON data into the struct
			if err := json.Unmarshal(alertBytes, &alertData); err != nil {
				fmt.Println("Ignoring alerts in 'Update Health'. Error unmarshalling alerts: %w", err)
			}
			updateInsights = append(updateInsights, parseAlertDataToInsights(alertData, startedAt)...)
		}
		cpStatusDisplayData, cpInsights = assessControlPlaneStatus(cv, operators.Items, now)
	} else {
		var us *configv1alpha1.UpdateStatus
		if us = o.mockData.updateStatus; o.statusApi && us == nil {
			var err error
			us, err = o.ConfigV1Alpha1Client.UpdateStatuses().Get(ctx, "cluster", metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					return fmt.Errorf("no update status information available - you must be connected to an OpenShift version 4 server to fetch the current version")
				}
				return err
			}
		}
		if o.statusApi && us == nil {
			return fmt.Errorf("status API usage enabled but UpdateStatus resource is missing")
		}
		controlPlaneUpdateProgressing := meta.FindStatusCondition(us.Status.ControlPlane.Conditions, string(configv1alpha1.ControlPlaneConditionTypeUpdating))
		controlPlaneUpdating = controlPlaneUpdateProgressing != nil && controlPlaneUpdateProgressing.Status == metav1.ConditionTrue

		var cvInsight *configv1alpha1.ClusterVersionStatusInsight
		for _, informer := range us.Status.ControlPlane.Informers {
			if cvInsight = findClusterVersionInsight(informer.Insights); cvInsight != nil {
				break
			}
		}

		if cvInsight == nil {
			return fmt.Errorf("no ClusterVersion insight")
		}

		startedAt = cvInsight.StartedAt.Time

		if o.mockData.updateStatus == nil {
			now = time.Now()
		} else {
			// mock "now" to be the latest time when something happened in the mocked data
			// add some nanoseconds to exercise rounding
			now = startedAt
			for _, condition := range us.Status.Conditions {
				if condition.LastTransitionTime.After(now) {
					now = condition.LastTransitionTime.Time.Add(368975 * time.Nanosecond)
				}
			}
			for _, condition := range us.Status.ControlPlane.Conditions {
				if condition.LastTransitionTime.After(now) {
					now = condition.LastTransitionTime.Time.Add(368975 * time.Nanosecond)
				}
			}
			for _, informer := range us.Status.ControlPlane.Informers {
				for _, insight := range informer.Insights {
					if insight.ClusterVersionStatusInsight != nil {
						if insight.ClusterVersionStatusInsight.StartedAt.After(now) {
							now = insight.ClusterVersionStatusInsight.StartedAt.Time.Add(368975 * time.Nanosecond)
						}
						if insight.ClusterVersionStatusInsight.CompletedAt.After(now) {
							now = insight.ClusterVersionStatusInsight.CompletedAt.Time.Add(368975 * time.Nanosecond)
						}
						for _, condition := range insight.ClusterVersionStatusInsight.Conditions {
							if condition.LastTransitionTime.After(now) {
								now = condition.LastTransitionTime.Time.Add(368975 * time.Nanosecond)
							}
						}
					}
				}
			}
		}

		cpStatusDisplayData.Assessment = assessmentState(cvInsight.Assessment)
		cpStatusDisplayData.Completion = float64(cvInsight.Completion)
		if updatingFor := now.Sub(startedAt).Round(time.Second); updatingFor > 10*time.Minute {
			cpStatusDisplayData.Duration = updatingFor.Round(time.Minute)
		} else {
			cpStatusDisplayData.Duration = updatingFor.Round(time.Second)
		}

		cpStatusDisplayData.TargetVersion.target = cvInsight.Versions.Target
		cpStatusDisplayData.TargetVersion.previous = cvInsight.Versions.Previous
		cpStatusDisplayData.TargetVersion.isTargetInstall = cvInsight.Versions.IsTargetInstall
		cpStatusDisplayData.TargetVersion.isPreviousPartial = cvInsight.Versions.IsPreviousPartial

		cpStatusDisplayData.CompletionAt = cvInsight.CompletedAt.Time
		cpStatusDisplayData.EstTimeToComplete = cvInsight.EstimatedCompletedAt.Time.Sub(now).Round(time.Second)
		cpStatusDisplayData.EstDuration = cvInsight.EstimatedCompletedAt.Time.Sub(startedAt).Round(time.Second)

		for _, informer := range us.Status.ControlPlane.Informers {
			for _, insight := range informer.Insights {
				if insight.Type == configv1alpha1.UpdateInsightTypeClusterOperatorStatusInsight {
					coInsight := insight.ClusterOperatorStatusInsight
					if updating := meta.FindStatusCondition(coInsight.Conditions, string(configv1alpha1.ClusterOperatorStatusInsightConditionTypeUpdating)); updating != nil {
						switch updating.Status {
						case metav1.ConditionTrue:
							cpStatusDisplayData.Operators.Updating++
						case metav1.ConditionFalse:
							switch updating.Reason {
							case string(configv1alpha1.ClusterOperatorStatusInsightUpdatingReasonUpdated):
								cpStatusDisplayData.Operators.Updated++
							case string(configv1alpha1.ClusterOperatorStatusInsightUpdatingReasonPending):
								cpStatusDisplayData.Operators.Waiting++
							default:
							}
						case metav1.ConditionUnknown:
						default:
						}
					}
					cpStatusDisplayData.Operators.Total++
					if healthy := meta.FindStatusCondition(coInsight.Conditions, string(configv1alpha1.ClusterOperatorStatusInsightConditionTypeHealthy)); healthy != nil {
						switch healthy.Status {
						case metav1.ConditionFalse:
							switch healthy.Reason {
							case string(configv1alpha1.ClusterOperatorUpdateStatusInsightHealthyReasonDegraded):
								cpStatusDisplayData.Operators.Degraded++
							case string(configv1alpha1.ClusterOperatorUpdateStatusInsightHealthyReasonUnavailable):
								cpStatusDisplayData.Operators.Unavailable++
							default:
							}
						case metav1.ConditionTrue:
						case metav1.ConditionUnknown:
						default:
						}
					}
				}
				if insight.Type == configv1alpha1.UpdateInsightTypeMachineConfigPoolStatusInsight {
					mcpInsight := insight.MachineConfigPoolStatusInsight
					displayDataFromMachineConfigPoolInsight(&controlPlanePoolStatusData, mcpInsight)
				}
				if insight.Type == configv1alpha1.UpdateInsightTypeNodeStatusInsight {
					nodeInsight := insight.NodeStatusInsight
					displayDataFromNodeInsight(&controlPlanePoolStatusData, nodeInsight)
				}
				if insight.Type == configv1alpha1.UpdateInsightTypeUpdateHealthInsight {
					updateInsights = append(updateInsights, toUpdateInsight(insight.UpdateHealthInsight))
				}
			}
		}

		for _, pool := range us.Status.WorkerPools {
			var pdd poolDisplayData
			for _, informer := range pool.Informers {
				for _, insight := range informer.Insights {
					switch insight.Type {
					case configv1alpha1.UpdateInsightTypeMachineConfigPoolStatusInsight:
						mcpInsight := insight.MachineConfigPoolStatusInsight
						displayDataFromMachineConfigPoolInsight(&pdd, mcpInsight)
					case configv1alpha1.UpdateInsightTypeNodeStatusInsight:
						nodeInsight := insight.NodeStatusInsight
						displayDataFromNodeInsight(&pdd, nodeInsight)
					case configv1alpha1.UpdateInsightTypeUpdateHealthInsight:
						updateInsights = append(updateInsights, toUpdateInsight(insight.UpdateHealthInsight))
					}
				}
			}
			workerPoolsStatusData = append(workerPoolsStatusData, pdd)
		}
	}

	for _, pool := range workerPoolsStatusData {
		if pool.NodesOverview.Total > 0 && pool.Completion != 100 {
			isWorkerPoolOutdated = true
			break
		}
	}

	if !controlPlaneUpdating && !isWorkerPoolOutdated {
		_, _ = fmt.Fprintf(o.Out, "The cluster is not updating.\n")
		return nil
	}

	updateInsights = append(updateInsights, cpInsights...)
	_ = cpStatusDisplayData.Write(o.Out)
	controlPlanePoolStatusData.WriteNodes(o.Out, o.enabledDetailed(detailedOutputNodes))

	_, _ = fmt.Fprintf(o.Out, "\n= Worker Upgrade =\n")
	writePools(o.Out, workerPoolsStatusData)
	for _, pool := range workerPoolsStatusData {
		pool.WriteNodes(o.Out, o.enabledDetailed(detailedOutputNodes))
	}

	updatingFor := now.Sub(startedAt).Round(time.Second)
	_, _ = fmt.Fprintf(o.Out, "\n")
	upgradeHealth, allowDetailed := assessUpdateInsights(updateInsights, updatingFor, now)
	_ = upgradeHealth.Write(o.Out, allowDetailed && o.enabledDetailed(detailedOutputHealth))
	return nil
}

func toUpdateInsight(apiInsight *configv1alpha1.UpdateHealthInsight) updateInsight {
	var resources []scopeResource
	for _, resource := range apiInsight.Scope.Resources {
		resources = append(resources, scopeResource{
			kind: scopeGroupKind{
				group: resource.APIGroup,
				kind:  resource.Kind,
			},
			name:      resource.Name,
			namespace: resource.Namespace,
		})
	}

	insight := updateInsight{
		startedAt: apiInsight.StartedAt.Time,
		scope: updateInsightScope{
			scopeType: scopeType(apiInsight.Scope.Type),
			resources: resources,
		},
		impact: updateInsightImpact{
			level:       toImpactLevel(apiInsight.Impact.Level),
			impactType:  impactType(apiInsight.Impact.Type),
			summary:     apiInsight.Impact.Summary,
			description: apiInsight.Impact.Description,
		},
		remediation: updateInsightRemediation{
			reference: apiInsight.Remediation.Reference,
		},
	}

	return insight
}

func toImpactLevel(level configv1alpha1.InsightImpactLevel) impactLevel {
	switch level {
	case configv1alpha1.InfoImpactLevel:
		return infoImpactLevel
	case configv1alpha1.WarningImpactLevel:
		return warningImpactLevel
	case configv1alpha1.ErrorImpactLevel:
		return errorImpactLevel
	case configv1alpha1.CriticalInfoLevel:
		return criticalInfoLevel
	}

	return infoImpactLevel
}

func displayDataFromNodeInsight(pdd *poolDisplayData, nodeInsight *configv1alpha1.NodeStatusInsight) {
	updating := meta.FindStatusCondition(nodeInsight.Conditions, string(configv1alpha1.NodeStatusInsightConditionTypeUpdating))
	available := meta.FindStatusCondition(nodeInsight.Conditions, string(configv1alpha1.NodeStatusInsightConditionTypeAvailable))
	degraded := meta.FindStatusCondition(nodeInsight.Conditions, string(configv1alpha1.NodeStatusInsightConditionTypeDegraded))

	ndd := nodeDisplayData{
		Name:    nodeInsight.Name,
		Version: nodeInsight.Version,
		Message: nodeInsight.Message,
	}

	zeroEst := "?" // unknown
	if updating != nil && updating.Status == metav1.ConditionTrue {
		ndd.isUpdating = true
		ndd.Assessment = nodeAssessmentProgressing
		switch updating.Reason {
		case string(configv1alpha1.NodeStatusInsightUpdatingReasonDraining):
			ndd.Phase = phaseStateDraining
		case string(configv1alpha1.NodeStatusInsightUpdatingReasonUpdating):
			ndd.Phase = phaseStateUpdating
		case string(configv1alpha1.NodeStatusInsightUpdatingReasonRebooting):
			ndd.Phase = phaseStateRebooting
		}

	}

	if updating != nil && updating.Status == metav1.ConditionFalse {
		ndd.isUpdating = false
		switch updating.Reason {
		case string(configv1alpha1.NodeStatusInsightUpdatingReasonPaused):
			ndd.Assessment = nodeAssessmentExcluded
			ndd.Phase = phaseStatePaused
			zeroEst = "-"
		case string(configv1alpha1.NodeStatusInsightUpdatingReasonCompleted):
			ndd.isUpdated = true
			ndd.Assessment = nodeAssessmentCompleted
			ndd.Phase = phaseStateUpdated
			zeroEst = "-"
		case string(configv1alpha1.NodeStatusInsightUpdatingReasonPending):
			ndd.Assessment = nodeAssessmentOutdated
			ndd.Phase = phaseStatePending
		}
	}

	if available != nil {
		ndd.isUnavailable = available.Status == metav1.ConditionFalse
	}

	if degraded != nil {
		ndd.isDegraded = degraded.Status == metav1.ConditionTrue
	}

	if nodeInsight.EstToComplete.Duration == 0 || ndd.isDegraded || ndd.isUnavailable {
		ndd.Estimate = zeroEst
	} else {
		ndd.Estimate = "+" + shortDuration(nodeInsight.EstToComplete.Duration)
	}

	pdd.Nodes = append(pdd.Nodes, ndd)
}

func displayDataFromMachineConfigPoolInsight(controlPlanePoolStatusData *poolDisplayData, mcpInsight *configv1alpha1.MachineConfigPoolStatusInsight) {
	controlPlanePoolStatusData.Name = mcpInsight.Name
	controlPlanePoolStatusData.Assessment = assessmentState(mcpInsight.Assessment)
	controlPlanePoolStatusData.Completion = float64(mcpInsight.Completion)
	for _, summary := range mcpInsight.Summaries {
		switch summary.Type {
		case configv1alpha1.PoolNodesSummaryTypeTotal:
			controlPlanePoolStatusData.NodesOverview.Total = int(summary.Count)
		case configv1alpha1.PoolNodesSummaryTypeAvailable:
			controlPlanePoolStatusData.NodesOverview.Available = int(summary.Count)
		case configv1alpha1.PoolNodesSummaryTypeProgressing:
			controlPlanePoolStatusData.NodesOverview.Progressing = int(summary.Count)
		case configv1alpha1.PoolNodesSummaryTypeOutdated:
			controlPlanePoolStatusData.NodesOverview.Outdated = int(summary.Count)
		case configv1alpha1.PoolNodesSummaryTypeDraining:
			controlPlanePoolStatusData.NodesOverview.Draining = int(summary.Count)
		case configv1alpha1.PoolNodesSummaryTypeExcluded:
			controlPlanePoolStatusData.NodesOverview.Excluded = int(summary.Count)
		case configv1alpha1.PoolNodesSummaryTypeDegraded:
			controlPlanePoolStatusData.NodesOverview.Degraded = int(summary.Count)
		}
	}
}

func findClusterOperatorStatusCondition(conditions []configv1.ClusterOperatorStatusCondition, name configv1.ClusterStatusConditionType) *configv1.ClusterOperatorStatusCondition {
	for i := range conditions {
		if conditions[i].Type == name {
			return &conditions[i]
		}
	}
	return nil
}

func findClusterVersionInsight(insights []configv1alpha1.UpdateInsight) *configv1alpha1.ClusterVersionStatusInsight {
	for i := range insights {
		if insights[i].Type == configv1alpha1.UpdateInsightTypeClusterVersionStatusInsight {
			return insights[i].ClusterVersionStatusInsight
		}
	}

	return nil
}
