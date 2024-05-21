package status

import (
	"fmt"
	"io"
	"math"
	"strings"
	"text/template"
	"time"

	v1 "github.com/openshift/api/config/v1"
)

type assessmentState string

const (
	assessmentStateProgressing assessmentState = "Progressing"
	assessmentStateCompleted   assessmentState = "Completed"
	assessmentStatePending     assessmentState = "Pending"
	assessmentStateExcluded    assessmentState = "Excluded"
	assessmentStateDegraded    assessmentState = "Degraded"

	// clusterStatusFailing is set on the ClusterVersion status when a cluster
	// cannot reach the desired state.
	clusterStatusFailing = v1.ClusterStatusConditionType("Failing")
)

type operators struct {
	Total       int
	Unavailable int
	// Degraded is the count of operators that are available but degraded
	Degraded int
}

func (o operators) StatusSummary() string {
	res := []string{fmt.Sprintf("%d Healthy", o.Total-o.Unavailable-o.Degraded)}
	if o.Unavailable > 0 {
		res = append(res, fmt.Sprintf("%d Unavailable", o.Unavailable))
	}
	if o.Degraded > 0 {
		res = append(res, fmt.Sprintf("%d Available but degraded", o.Degraded))
	}
	return strings.Join(res, ", ")
}

type versions struct {
	target            string
	previous          string
	isTargetInstall   bool
	isPreviousPartial bool
}

func (v versions) String() string {
	if v.isTargetInstall {
		return fmt.Sprintf("%s (a new install)", v.target)
	}
	if v.isPreviousPartial {
		return fmt.Sprintf("%s (from incomplete %s)", v.target, v.previous)
	}
	return fmt.Sprintf("%s (from %s)", v.target, v.previous)
}

type controlPlaneStatusDisplayData struct {
	Assessment        assessmentState
	Completion        float64
	Duration          time.Duration
	EstCompletion     time.Time
	EstTimeToComplete time.Duration
	Operators         operators
	TargetVersion     versions
}

const (
	unavailableWarningThreshold = 5 * time.Minute
	unavailableErrorThreshold   = 20 * time.Minute
	degradedWarningThreshold    = 5 * time.Minute
	degradedErrorThreshold      = 40 * time.Minute
)

func coInsights(name string, available *v1.ClusterOperatorStatusCondition, degraded *v1.ClusterOperatorStatusCondition, evaluated time.Time) []updateInsight {
	coGroupKind := scopeGroupKind{group: v1.GroupName, kind: "ClusterOperator"}
	var insights []updateInsight
	if available != nil && available.Status == v1.ConditionFalse && evaluated.After(available.LastTransitionTime.Time.Add(unavailableWarningThreshold)) {
		insight := updateInsight{
			startedAt: available.LastTransitionTime.Time,
			scope:     updateInsightScope{scopeType: scopeTypeControlPlane, resources: []scopeResource{{kind: coGroupKind, name: name}}},
			impact: updateInsightImpact{
				level:       warningImpactLevel,
				impactType:  apiAvailabilityImpactType,
				summary:     fmt.Sprintf("Cluster Operator %s is unavailable (%s)", name, available.Reason),
				description: available.Message,
			},
			remediation: updateInsightRemediation{reference: "https://github.com/openshift/runbooks/blob/master/alerts/cluster-monitoring-operator/ClusterOperatorDown.md"},
		}
		if available.Message == "" {
			// Backfill the description if CO doesn't provide one
			insight.impact.description = "<no message>"
		}
		if evaluated.After(available.LastTransitionTime.Time.Add(unavailableErrorThreshold)) {
			insight.impact.level = errorImpactLevel
		}
		insights = append(insights, insight)
	}
	if degraded != nil && degraded.Status == v1.ConditionTrue && evaluated.After(degraded.LastTransitionTime.Time.Add(degradedWarningThreshold)) {
		insight := updateInsight{
			startedAt: degraded.LastTransitionTime.Time,
			scope:     updateInsightScope{scopeType: scopeTypeControlPlane, resources: []scopeResource{{kind: coGroupKind, name: name}}},
			impact: updateInsightImpact{
				level:       warningImpactLevel,
				impactType:  apiAvailabilityImpactType,
				summary:     fmt.Sprintf("Cluster Operator %s is degraded (%s)", name, degraded.Reason),
				description: degraded.Message,
			},
			remediation: updateInsightRemediation{reference: "https://github.com/openshift/runbooks/blob/master/alerts/cluster-monitoring-operator/ClusterOperatorDegraded.md"},
		}
		if degraded.Message == "" {
			// Backfill the description if CO doesn't provide one
			insight.impact.description = "<no message>"
		}
		if evaluated.After(degraded.LastTransitionTime.Time.Add(degradedErrorThreshold)) {
			insight.impact.level = errorImpactLevel
		}
		insights = append(insights, insight)
	}
	return insights
}

func assessControlPlaneStatus(cv *v1.ClusterVersion, operators []v1.ClusterOperator, at time.Time) (controlPlaneStatusDisplayData, []updateInsight) {
	var displayData controlPlaneStatusDisplayData
	var completed int
	var insights []updateInsight

	targetVersion := cv.Status.Desired.Version
	cvGvk := cv.GroupVersionKind()
	cvGroupKind := scopeGroupKind{group: cvGvk.Group, kind: cvGvk.Kind}
	cvScope := scopeResource{kind: cvGroupKind, name: cv.Name}

	if c := findClusterOperatorStatusCondition(cv.Status.Conditions, clusterStatusFailing); c == nil {
		insight := updateInsight{
			startedAt: at,
			scope:     updateInsightScope{scopeType: scopeTypeControlPlane, resources: []scopeResource{cvScope}},
			impact: updateInsightImpact{
				level:       warningImpactLevel,
				impactType:  updateStalledImpactType,
				summary:     fmt.Sprintf("Cluster Version %s has no %s condition", cv.Name, clusterStatusFailing),
				description: "Current status of Cluster Version reconciliation is unclear.  See 'oc -n openshift-cluster-version logs -l k8s-app=cluster-version-operator --tail -1' to debug.",
			},
			remediation: updateInsightRemediation{reference: "https://github.com/openshift/runbooks/blob/master/alerts/cluster-monitoring-operator/ClusterOperatorDegraded.md"},
		}
		insights = append(insights, insight)
	} else if c.Status != v1.ConditionFalse {
		insight := updateInsight{
			startedAt: c.LastTransitionTime.Time,
			scope:     updateInsightScope{scopeType: scopeTypeControlPlane, resources: []scopeResource{{kind: cvGroupKind, name: cv.Name}}},
			impact: updateInsightImpact{
				level:       warningImpactLevel,
				impactType:  updateStalledImpactType,
				summary:     fmt.Sprintf("Cluster Version %s is failing to proceed with the update (%s)", cv.Name, c.Reason),
				description: c.Message,
			},
			remediation: updateInsightRemediation{reference: "https://github.com/openshift/runbooks/blob/master/alerts/cluster-monitoring-operator/ClusterOperatorDegraded.md"},
		}
		insights = append(insights, insight)
	}

	var lastObservedProgress time.Time
	for _, operator := range operators {
		var isPlatformOperator bool
		for annotation := range operator.Annotations {
			if strings.HasPrefix(annotation, "exclude.release.openshift.io/") ||
				strings.HasPrefix(annotation, "include.release.openshift.io/") {
				isPlatformOperator = true
				break
			}
		}
		if !isPlatformOperator {
			continue
		}

		var updated bool
		for _, version := range operator.Status.Versions {
			if version.Name == "operator" && version.Version == targetVersion {
				updated = true
				break
			}
		}

		if updated {
			completed++
		}

		var available *v1.ClusterOperatorStatusCondition
		var degraded *v1.ClusterOperatorStatusCondition
		var progressing *v1.ClusterOperatorStatusCondition

		displayData.Operators.Total++
		for _, condition := range operator.Status.Conditions {
			condition := condition
			switch {
			case condition.Type == v1.OperatorAvailable:
				available = &condition
			case condition.Type == v1.OperatorDegraded:
				degraded = &condition
			case condition.Type == v1.OperatorProgressing:
				progressing = &condition
			}
		}

		if progressing != nil {
			if progressing.Status == v1.ConditionTrue && !updated || progressing.Status == v1.ConditionFalse && updated {
				if progressing.LastTransitionTime.After(lastObservedProgress) {
					lastObservedProgress = progressing.LastTransitionTime.Time
				}
			}
		}

		if available == nil || available.Status != v1.ConditionTrue {
			displayData.Operators.Unavailable++
		} else if degraded != nil && degraded.Status == v1.ConditionTrue {
			displayData.Operators.Degraded++
		}
		insights = append(insights, coInsights(operator.Name, available, degraded, at)...)
	}

	controlPlaneCompleted := completed == displayData.Operators.Total
	if controlPlaneCompleted {
		displayData.Assessment = assessmentStateCompleted
	} else {
		displayData.Assessment = assessmentStateProgressing
	}

	var updatingFor time.Duration

	if len(cv.Status.History) > 0 {
		currentHistoryItem := cv.Status.History[0]
		started := currentHistoryItem.StartedTime.Time
		if currentHistoryItem.State == v1.CompletedUpdate {
			updatingFor = currentHistoryItem.CompletionTime.Time.Sub(started)
		} else {
			updatingFor = at.Sub(started)
		}
		// precision to seconds when under 60s
		if updatingFor > 10*time.Minute {
			displayData.Duration = updatingFor.Round(time.Minute)
		} else {
			displayData.Duration = updatingFor.Round(time.Second)
		}

	}

	versionData, versionInsights := versionsFromHistory(cv.Status.History, cvScope, controlPlaneCompleted)
	displayData.TargetVersion = versionData
	insights = append(insights, versionInsights...)

	coCompletion := float64(completed) / float64(displayData.Operators.Total)
	displayData.Completion = coCompletion * 100.0
	if estimate := estimateCompletion(updatingFor, coCompletion); estimate > 10*time.Minute {
		displayData.EstTimeToComplete = estimate.Round(time.Minute)
	} else {
		displayData.EstTimeToComplete = estimate.Round(time.Second)
	}
	return displayData, insights
}

func estimateCompletion(updatingFor time.Duration, coCompletion float64) time.Duration {
	if !(coCompletion > 0) {
		return time.Hour
	}
	if coCompletion >= 1 {
		return 0
	}

	completion := timewiseComplete(coCompletion)
	elapsedSeconds := updatingFor.Seconds()
	estimateTotalSeconds := elapsedSeconds / completion
	remainingSeconds := estimateTotalSeconds - elapsedSeconds
	estimateTimeToComplete := time.Duration(remainingSeconds) * time.Second

	return estimateTimeToComplete
}

// timewiseComplete returns the estimated timewise completion given the cluster operator completion percentage
// Typical cluster achieves 97% cluster operator completion in 67% of the time it takes to reach 100% completion
// The function is a combination of 3 polynomial functions that approximate the curve of the completion percentage
// The polynomes were obtained by curve fitting update progress on b01 cluster
func timewiseComplete(coCompletion float64) float64 {
	x := coCompletion
	x2 := math.Pow(x, 2)
	x3 := math.Pow(x, 3)
	x4 := math.Pow(x, 4)
	switch {
	case coCompletion < 0.25:
		return -0.03078788 + 2.62886*x - 3.823954*x2
	case coCompletion < 0.9:
		return 0.1851215 + 1.64994*x - 4.676898*x2 + 5.451824*x3 - 2.125286*x4
	default: // >0.9
		return 25053.32 - 107394.3*x + 172527.2*x2 - 123107*x3 + 32921.81*x4
	}
}

func versionsFromHistory(history []v1.UpdateHistory, cvScope scopeResource, controlPlaneCompleted bool) (versions, []updateInsight) {
	versionData := versions{
		target:   "unknown",
		previous: "unknown",
	}
	if len(history) > 0 {
		versionData.target = history[0].Version
	}
	if len(history) == 1 {
		versionData.isTargetInstall = true
		return versionData, nil
	}
	if len(history) > 1 {
		versionData.previous = history[1].Version
		versionData.isPreviousPartial = history[1].State == v1.PartialUpdate
	}

	var insights []updateInsight
	if !controlPlaneCompleted && versionData.isPreviousPartial {
		lastComplete := "unknown"
		if len(history) > 2 {
			for _, item := range history[2:] {
				if item.State == v1.CompletedUpdate {
					lastComplete = item.Version
					break
				}
			}
		}
		insights = []updateInsight{
			{
				startedAt: history[0].StartedTime.Time,
				scope: updateInsightScope{
					scopeType: scopeTypeControlPlane,
					resources: []scopeResource{cvScope},
				},
				impact: updateInsightImpact{
					level:       warningImpactLevel,
					impactType:  noneImpactType,
					summary:     fmt.Sprintf("Previous update to %s never completed, last complete update was %s", versionData.previous, lastComplete),
					description: fmt.Sprintf("Current update to %s was initiated while the previous update to version %s was still in progress", versionData.target, versionData.previous),
				},
				remediation: updateInsightRemediation{
					reference: "https://docs.openshift.com/container-platform/latest/updating/troubleshooting_updates/gathering-data-cluster-update.html#gathering-clusterversion-history-cli_troubleshooting_updates",
				},
			},
		}
	}

	return versionData, insights
}

var controlPlaneStatusTemplate = template.Must(
	template.New("controlPlaneStatus").
		Funcs(template.FuncMap{"shortDuration": shortDuration}).
		Parse(controlPlaneStatusTemplateRaw))

func (d *controlPlaneStatusDisplayData) Write(f io.Writer) error {
	return controlPlaneStatusTemplate.Execute(f, d)
}

const controlPlaneStatusTemplateRaw = `= Control Plane =
Assessment:      {{ .Assessment }}
Target Version:  {{ .TargetVersion }}
Completion:      {{ printf "%.0f" .Completion }}%
Duration:        {{ shortDuration .Duration }} (Est Time Remaining: {{ shortDuration .EstTimeToComplete }})
Operator Status: {{ .Operators.StatusSummary }}
`
