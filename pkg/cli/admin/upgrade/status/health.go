package status

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
)

type scopeType string

const (
	scopeTypeControlPlane scopeType = "ControlPlane"
	scopeTypeWorkerPool   scopeType = "WorkerPool"
)

type allowedScopeKind string

const (
	scopeKindClusterOperator   allowedScopeKind = "ClusterOperator"
	scopeKindNode              allowedScopeKind = "Node"
	scopeKindMachineConfigPool allowedScopeKind = "MachineConfigPool"
)

type scopeResource struct {
	kind      allowedScopeKind
	namespace string
	name      string
}

func (r scopeResource) namespacedName() string {
	if r.namespace == "" {
		return r.name
	} else {
		return fmt.Sprintf("%s/%s", r.namespace, r.name)
	}
}

type updateInsightScope struct {
	scopeType scopeType
	resources []scopeResource
}

type impactLevel uint32

// TODO(muller/OTA): Revise these levels when we move these structures to server-side. As of Jan 2024 we are not
// entirely sure whether we would need `critical` level or not.
const (
	// infoImpactLevel should be used for insights that are strictly informational or even positive (things go well or
	// something recently healed)
	infoImpactLevel impactLevel = iota
	// warningImpactLevel should be used for insights that explain a minor or transient problem. Anything that requires
	// admin attention or manual action should not be a warning but at least an error.
	warningImpactLevel
	// errorImpactLevel should be used for insights that inform about a problem that requires admin attention. Insights of
	// level error and higher should be as actionable as possible, and should be accompanied by links to documentation,
	// KB articles or other resources that help the admin to resolve the problem.
	errorImpactLevel
	// criticalInfoLevel should be used rarely, for insights that inform about a severe problem, threatening with data
	// loss, destroyed cluster or other catastrophic consequences. Insights of this level should be accompanied by
	// links to documentation, KB articles or other resources that help the admin to resolve the problem, or at least
	// prevent the severe consequences from happening.
	criticalInfoLevel
)

func (l impactLevel) String() string {
	switch l {
	case infoImpactLevel:
		return "Info"
	case warningImpactLevel:
		return "Warning"
	case errorImpactLevel:
		return "Error"
	case criticalInfoLevel:
		return "Critical"
	default:
		return "Unknown"
	}
}

type impactType string

// TODO(muller/OTA): Revise these consts when we move these structures to server-side. These constants were proposed
// by Justin in 'OpenShift Update Concepts' slides that serve as a basis for this effort but we never properly
// considered whether these are exactly the ones that we need.
const (
	noneImpactType                    impactType = "None"
	apiAvailabilityImpactType         impactType = "API Availability"
	clusterCapacityImpactType         impactType = "Cluster Capacity"
	applicationAvailabilityImpactType impactType = "Application Availability"
	applicationOutageImpactType       impactType = "Application Outage"
	dataLossImpactType                impactType = "Data Loss"
	updateSpeedImpactType             impactType = "Update Speed"
	updateStalledImpactType           impactType = "Update Stalled"
)

type updateInsightImpact struct {
	level       impactLevel
	impactType  impactType
	summary     string
	description string
}

func (i updateInsightImpact) incomplete() bool {
	return i.description == "" || i.summary == ""
}

type updateInsightRemediation struct {
	reference string
}

func (r updateInsightRemediation) incomplete() bool {
	return r.reference == ""
}

type updateInsight struct {
	startedAt   time.Time
	scope       updateInsightScope
	impact      updateInsightImpact
	remediation updateInsightRemediation
}

func (i updateInsight) incomplete() bool {
	return i.impact.incomplete() || i.remediation.incomplete()
}

type updateHealthData struct {
	evaluatedAt time.Time
	insights    []updateInsight
}

// assessUpdateInsights processes insights to be displayed and returns matching displayable data. If the displayable data are not
// worth showing in detailed mode, returns false as the second return value.
func assessUpdateInsights(insights []updateInsight, upgradingFor time.Duration, evaluatedAt time.Time) (updateHealthData, bool) {
	sorted := make([]updateInsight, 0, len(insights))
	for _, insight := range insights {
		if insight.incomplete() {
			continue
		}
		sorted = append(sorted, insight)
	}

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].startedAt.After(sorted[j].startedAt)
	})
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].impact.level > sorted[j].impact.level
	})

	if len(sorted) == 0 {
		return updateHealthData{
			evaluatedAt: evaluatedAt,
			insights: []updateInsight{
				{
					startedAt: evaluatedAt.Add(-upgradingFor),
					impact: updateInsightImpact{
						level:      infoImpactLevel,
						impactType: noneImpactType,
						summary:    "Upgrade is proceeding well",
					},
				},
			},
		}, false
	}

	return updateHealthData{
		evaluatedAt: evaluatedAt,
		insights:    sorted,
	}, true
}

func shortDuration(d time.Duration) string {
	orig := d.String()
	switch {
	case orig == "0h0m0s":
		return "now"
	case strings.HasSuffix(orig, "0m0s"):
		return orig[:len(orig)-4]
	case strings.HasSuffix(orig, "m0s"):
		return orig[:len(orig)-2]
	case strings.HasSuffix(orig, "h0m"):
		return orig[:len(orig)-2]
	}
	return orig
}

func stringSince(health *updateHealthData, insight updateInsight) string {
	if insight.startedAt.IsZero() {
		// On client it is not possible to calculate the precise time of all insights
		// On server side we can backfill with firstObserved
		return "-"
	}
	return shortDuration(health.evaluatedAt.Sub(insight.startedAt).Truncate(time.Second))
}

type displayItem struct {
	since       string
	level       string
	impact      string
	message     string
	description string
	reference   string
	resources   []scopeResource
}

func (i *updateHealthData) Write(w io.Writer, detailed bool) error {
	_, _ = w.Write([]byte("= Update Health =\n"))

	displayData := make([]displayItem, 0, len(i.insights))

	for _, insight := range i.insights {
		displayData = append(displayData, displayItem{
			since:       stringSince(i, insight),
			level:       insight.impact.level.String(),
			impact:      string(insight.impact.impactType),
			message:     insight.impact.summary,
			description: insight.impact.description,
			reference:   insight.remediation.reference,
			resources:   insight.scope.resources,
		})
	}
	if detailed {
		detailedOutput(w, displayData)
	} else {
		tabulatedOutput(w, displayData)
	}

	return nil
}

func detailedResourceOutput(w io.Writer, resources []scopeResource) {
	if len(resources) == 0 {
		return
	}
	_, _ = w.Write([]byte(fmt.Sprintf("  %s\n", "Resources:")))
	var pad int
	byKind := make(map[allowedScopeKind][]string)

	kinds := sets.New[allowedScopeKind]()
	for _, resource := range resources {
		byKind[resource.kind] = append(byKind[resource.kind], resource.namespacedName())
		kinds.Insert(resource.kind)
		if n := len(resource.kind); n > pad {
			pad = n
		}
	}

	for _, kind := range sets.List(kinds) {
		_, _ = w.Write([]byte(fmt.Sprintf("    %-*ss: ", pad, kind)))
		sort.Strings(byKind[kind])
		_, _ = w.Write([]byte(strings.Join(byKind[kind], " ") + "\n"))
	}

}

func detailedOutput(w io.Writer, items []displayItem) {
	pad := len("Description: ")
	for i, item := range items {
		_, _ = w.Write([]byte(fmt.Sprintf("Message: %s\n", item.message)))
		_, _ = w.Write([]byte(fmt.Sprintf("  %-*s%s\n", pad, "Since:", item.since)))
		_, _ = w.Write([]byte(fmt.Sprintf("  %-*s%s\n", pad, "Level:", item.level)))
		_, _ = w.Write([]byte(fmt.Sprintf("  %-*s%s\n", pad, "Impact:", item.impact)))
		_, _ = w.Write([]byte(fmt.Sprintf("  %-*s%s\n", pad, "Reference:", item.reference)))

		detailedResourceOutput(w, item.resources)
		// Respect the "  Description: " indentation when description has linebreaks
		item.description = strings.ReplaceAll(item.description, "\n", fmt.Sprintf("\n%s, ", strings.Repeat(" ", pad+2)))
		_, _ = w.Write([]byte(fmt.Sprintf("  %-*s%s\n", pad, "Description:", item.description)))
		if len(items) > i+1 {
			_, _ = w.Write([]byte("\n"))
		}
	}
}

func tabulatedOutput(w io.Writer, items []displayItem) {
	tabw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	_, _ = tabw.Write([]byte("SINCE\tLEVEL\tIMPACT\tMESSAGE\n"))
	for _, item := range items {
		_, _ = tabw.Write([]byte(item.since + "\t"))
		_, _ = tabw.Write([]byte(item.level + "\t"))
		_, _ = tabw.Write([]byte(item.impact + "\t"))
		_, _ = tabw.Write([]byte(item.message + "\n"))
	}
	tabw.Flush()
}
