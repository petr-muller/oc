// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1alpha1

import (
	v1 "k8s.io/client-go/applyconfigurations/meta/v1"
)

// UpdateStatusStatusApplyConfiguration represents an declarative configuration of the UpdateStatusStatus type for use
// with apply.
type UpdateStatusStatusApplyConfiguration struct {
	ControlPlane *ControlPlaneUpdateStatusApplyConfiguration `json:"controlPlane,omitempty"`
	WorkerPools  []PoolUpdateStatusApplyConfiguration        `json:"workerPools,omitempty"`
	Conditions   []v1.ConditionApplyConfiguration            `json:"conditions,omitempty"`
}

// UpdateStatusStatusApplyConfiguration constructs an declarative configuration of the UpdateStatusStatus type for use with
// apply.
func UpdateStatusStatus() *UpdateStatusStatusApplyConfiguration {
	return &UpdateStatusStatusApplyConfiguration{}
}

// WithControlPlane sets the ControlPlane field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ControlPlane field is set to the value of the last call.
func (b *UpdateStatusStatusApplyConfiguration) WithControlPlane(value *ControlPlaneUpdateStatusApplyConfiguration) *UpdateStatusStatusApplyConfiguration {
	b.ControlPlane = value
	return b
}

// WithWorkerPools adds the given value to the WorkerPools field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the WorkerPools field.
func (b *UpdateStatusStatusApplyConfiguration) WithWorkerPools(values ...*PoolUpdateStatusApplyConfiguration) *UpdateStatusStatusApplyConfiguration {
	for i := range values {
		if values[i] == nil {
			panic("nil value passed to WithWorkerPools")
		}
		b.WorkerPools = append(b.WorkerPools, *values[i])
	}
	return b
}

// WithConditions adds the given value to the Conditions field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Conditions field.
func (b *UpdateStatusStatusApplyConfiguration) WithConditions(values ...*v1.ConditionApplyConfiguration) *UpdateStatusStatusApplyConfiguration {
	for i := range values {
		if values[i] == nil {
			panic("nil value passed to WithConditions")
		}
		b.Conditions = append(b.Conditions, *values[i])
	}
	return b
}
