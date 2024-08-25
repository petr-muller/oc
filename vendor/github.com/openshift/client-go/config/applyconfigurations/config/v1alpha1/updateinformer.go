// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1alpha1

// UpdateInformerApplyConfiguration represents an declarative configuration of the UpdateInformer type for use
// with apply.
type UpdateInformerApplyConfiguration struct {
	Name     *string                           `json:"name,omitempty"`
	Insights []UpdateInsightApplyConfiguration `json:"insights,omitempty"`
}

// UpdateInformerApplyConfiguration constructs an declarative configuration of the UpdateInformer type for use with
// apply.
func UpdateInformer() *UpdateInformerApplyConfiguration {
	return &UpdateInformerApplyConfiguration{}
}

// WithName sets the Name field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Name field is set to the value of the last call.
func (b *UpdateInformerApplyConfiguration) WithName(value string) *UpdateInformerApplyConfiguration {
	b.Name = &value
	return b
}

// WithInsights adds the given value to the Insights field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Insights field.
func (b *UpdateInformerApplyConfiguration) WithInsights(values ...*UpdateInsightApplyConfiguration) *UpdateInformerApplyConfiguration {
	for i := range values {
		if values[i] == nil {
			panic("nil value passed to WithInsights")
		}
		b.Insights = append(b.Insights, *values[i])
	}
	return b
}
