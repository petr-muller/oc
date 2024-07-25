package status

import (
	"fmt"
	"os"

	configv1 "github.com/openshift/api/config/v1"
	configv1alpha1 "github.com/openshift/api/config/v1alpha1"
	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

type mockData struct {
	cvPath                 string
	operatorsPath          string
	machineConfigPoolsPath string
	machineConfigsPath     string
	nodesPath              string
	alertsPath             string
	updateStatusPath       string
	clusterVersion         *configv1.ClusterVersion
	clusterOperators       *configv1.ClusterOperatorList
	machineConfigPools     *machineconfigv1.MachineConfigPoolList
	machineConfigs         *machineconfigv1.MachineConfigList
	nodes                  *corev1.NodeList
	updateStatus           *configv1alpha1.UpdateStatus
}

func asResourceList[T any](objects *corev1.List, decoder runtime.Decoder) ([]T, error) {
	var outputItems []T
	for i, item := range objects.Items {
		obj, err := runtime.Decode(decoder, item.Raw)
		if err != nil {
			return nil, err
		}
		typedObj, ok := any(obj).(*T)
		if !ok {
			return nil, fmt.Errorf("unexpected object type %T in List content at index %d", obj, i)
		}
		outputItems = append(outputItems, *typedObj)
	}
	return outputItems, nil
}

func (o *mockData) load() error {
	scheme := runtime.NewScheme()
	codecs := serializer.NewCodecFactory(scheme)
	if err := configv1.Install(scheme); err != nil {
		return err
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		return err
	}
	if err := machineconfigv1.Install(scheme); err != nil {
		return err
	}
	if err := configv1alpha1.Install(scheme); err != nil {
		return err
	}
	decoder := codecs.UniversalDecoder(configv1.GroupVersion, corev1.SchemeGroupVersion, machineconfigv1.GroupVersion, configv1alpha1.GroupVersion)

	cvBytes, err := os.ReadFile(o.cvPath)
	if err != nil {
		return err
	}
	cvObj, err := runtime.Decode(decoder, cvBytes)
	if err != nil {
		return err
	}
	switch cvObj := cvObj.(type) {
	case *configv1.ClusterVersion:
		o.clusterVersion = cvObj
	case *configv1.ClusterVersionList:
		o.clusterVersion = &cvObj.Items[0]
	case *corev1.List:
		cvs, err := asResourceList[configv1.ClusterVersion](cvObj, decoder)
		if err != nil {
			return fmt.Errorf("error while parsing file %s: %w", o.cvPath, err)
		}
		o.clusterVersion = &cvs[0]
	default:
		return fmt.Errorf("unexpected object type %T in --mock-clusterversion=%s content", cvObj, o.cvPath)
	}

	if o.updateStatusPath != "" {
		usBytes, err := os.ReadFile(o.updateStatusPath)
		if err != nil {
			return err
		}
		usObj, err := runtime.Decode(decoder, usBytes)
		if err != nil {
			return err
		}
		switch usObj := usObj.(type) {
		case *configv1alpha1.UpdateStatus:
			o.updateStatus = usObj
		case *configv1alpha1.UpdateStatusList:
			o.updateStatus = &usObj.Items[0]
		case *corev1.List:
			uss, err := asResourceList[configv1alpha1.UpdateStatus](usObj, decoder)
			if err != nil {
				return fmt.Errorf("error while parsing file %s: %w", o.updateStatusPath, err)
			}
			o.updateStatus = &uss[0]
		default:
			return fmt.Errorf("unexpected object type %T in --mock-updatestatus=%s content", usObj, o.updateStatusPath)
		}
	}

	coListBytes, err := os.ReadFile(o.operatorsPath)
	if err != nil {
		return err
	}
	coListObj, err := runtime.Decode(decoder, coListBytes)
	if err != nil {
		return err
	}
	switch coListObj := coListObj.(type) {
	case *configv1.ClusterOperatorList:
		o.clusterOperators = coListObj
	case *corev1.List:
		cos, err := asResourceList[configv1.ClusterOperator](coListObj, decoder)
		if err != nil {
			return fmt.Errorf("error while parsing file %s: %w", o.operatorsPath, err)
		}
		o.clusterOperators = &configv1.ClusterOperatorList{
			Items: cos,
		}
	default:
		return fmt.Errorf("unexpected object type %T in --mock-clusteroperators=%s content", coListObj, o.operatorsPath)
	}

	nodeListBytes, err := os.ReadFile(o.nodesPath)
	if err != nil {
		return err
	}
	nodeListObj, err := runtime.Decode(decoder, nodeListBytes)
	if err != nil {
		return err
	}
	switch nodeListObj := nodeListObj.(type) {
	case *corev1.NodeList:
		o.nodes = nodeListObj
	case *corev1.List:
		nodes, err := asResourceList[corev1.Node](nodeListObj, decoder)
		if err != nil {
			return fmt.Errorf("error while parsing file %s: %w", o.nodesPath, err)
		}
		o.nodes = &corev1.NodeList{
			Items: nodes,
		}
	default:
		return fmt.Errorf("unexpected object type %T in --mock-nodes=%s content", nodeListObj, o.nodesPath)
	}

	mcpListBytes, err := os.ReadFile(o.machineConfigPoolsPath)
	if err != nil {
		return err
	}
	mcpListObj, err := runtime.Decode(decoder, mcpListBytes)
	if err != nil {
		return err
	}
	switch mcpListObj := mcpListObj.(type) {
	case *machineconfigv1.MachineConfigPoolList:
		o.machineConfigPools = mcpListObj
	case *corev1.List:
		mcps, err := asResourceList[machineconfigv1.MachineConfigPool](mcpListObj, decoder)
		if err != nil {
			return fmt.Errorf("error while parsing file %s: %w", o.machineConfigPoolsPath, err)
		}
		o.machineConfigPools = &machineconfigv1.MachineConfigPoolList{
			Items: mcps,
		}
	default:
		return fmt.Errorf("unexpected object type %T in --mock-machineconfigpools=%s content", mcpListObj, o.machineConfigPoolsPath)
	}

	mcListBytes, err := os.ReadFile(o.machineConfigsPath)
	if err != nil {
		return err
	}
	mcListObj, err := runtime.Decode(decoder, mcListBytes)
	if err != nil {
		return err
	}
	switch mcListObj := mcListObj.(type) {
	case *machineconfigv1.MachineConfigList:
		o.machineConfigs = mcListObj
	case *corev1.List:
		mcs, err := asResourceList[machineconfigv1.MachineConfig](mcListObj, decoder)
		if err != nil {
			return fmt.Errorf("error while parsing file %s: %w", o.machineConfigsPath, err)
		}
		o.machineConfigs = &machineconfigv1.MachineConfigList{
			Items: mcs,
		}
	default:
		return fmt.Errorf("unexpected object type %T in --mock-machineconfigs=%s content", mcpListObj, o.machineConfigsPath)
	}
	return nil
}
