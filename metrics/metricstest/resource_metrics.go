/*
Copyright 2020 The Knative Authors

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

// Package metricstest simplifies some of the common boilerplate around testing
// metrics exports. It should work with or without the code in metrics, but this
// code particularly knows how to deal with metrics which are exported for
// multiple Resources in the same process.
package metricstest

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"go.opencensus.io/metric/metricdata"
	"go.opencensus.io/metric/metricproducer"
	"go.opencensus.io/resource"
)

// Value provides a simplified implementation of a metric Value suitable for
// easy testing.
type Value struct {
	Tags map[string]string
	// union interface, only one of these will be set
	Int64        *int64
	Float64      *float64
	Distribution *metricdata.Distribution
}

// Metric provides a simplified (for testing) implementation of a metric report
// for a given metric name in a given Resource.
type Metric struct {
	// Name is the exported name of the metric, probably from the View's name.
	Name string
	// Unit is the units of measure of the metric. This is only checked for
	// equality if Unit is non-empty or VerifyMetadata is true on both Metrics.
	Unit metricdata.Unit
	// Type is the type of measurement represented by the metric. This is only
	// checked for equality if VerifyMetadata is true on both Metrics.
	Type metricdata.Type

	// Resource is the reported Resource (if any) for this metric. This is only
	// checked for equality if Resource is non-nil or VerifyResource is true on
	// both Metrics.
	Resource *resource.Resource

	// Values contains the values recorded for different Key=Value Tag
	// combinations. Value is checked for equality if present.
	Values []Value

	// Equality testing/validation settings on the Metric. These are used to
	// allow simple construction and usage with github.com/google/go-cmp/cmp

	// VerifyMetadata makes Equal compare Unit and Type if it is true on both
	// Metrics.
	VerifyMetadata bool
	// VerifyResource makes Equal compare Resource if it is true on Metrics with
	// nil Resource. Metrics with non-nil Resource are always compared.
	VerifyResource bool
}

// NewMetric creates a Metric from a metricdata.Metric, which is designed for
// compact wire representation.
func NewMetric(metric *metricdata.Metric) Metric {
	value := Metric{
		Name:     metric.Descriptor.Name,
		Unit:     metric.Descriptor.Unit,
		Type:     metric.Descriptor.Type,
		Resource: metric.Resource,

		VerifyMetadata: true,
		VerifyResource: true,

		Values: make([]Value, 0, len(metric.TimeSeries)),
	}

	for _, ts := range metric.TimeSeries {
		tags := make(map[string]string, len(metric.Descriptor.LabelKeys))
		for i, k := range metric.Descriptor.LabelKeys {
			tags[k.Key] = ts.LabelValues[i].Value
		}
		v := Value{Tags: tags}
		ts.Points[0].ReadValue(&v)
		value.Values = append(value.Values, v)
	}

	return value
}

// GetMetric returns all values for the named metric.
func GetMetric(name string) []Metric {
	producers := metricproducer.GlobalManager().GetAll()
	retval := make([]Metric, 0, len(producers))
	for _, p := range producers {
		for _, m := range p.Read() {
			if m.Descriptor.Name == name && len(m.TimeSeries) > 0 {
				retval = append(retval, NewMetric(m))
			}
		}
	}
	return retval
}

// GetOneMetric is like GetMetric, but it panics if more than a single Metric is
// found.
func GetOneMetric(name string) Metric {
	m := GetMetric(name)
	if len(m) != 1 {
		panic(fmt.Sprint("Got wrong number of metrics:", m))
	}
	return m[0]
}

func genericMetricFactory(name string, v Value, keyvalues ...string) Metric {
	if len(keyvalues)%2 != 0 {
		panic("Odd number of arguments to CountMetric")
	}
	if v.Tags == nil {
		v.Tags = make(map[string]string, len(keyvalues)/2)
	}
	for i := 0; i < len(keyvalues); i += 2 {
		v.Tags[keyvalues[i]] = keyvalues[i+1]
	}
	return Metric{
		Name:   name,
		Values: []Value{v},
	}
}

// IntMetric is a shortcut factory for creating an Int64 metric.
func IntMetric(name string, value int64, keyvalues ...string) Metric {
	return genericMetricFactory(name, Value{Int64: &value}, keyvalues...)
}

// FloatMetric is a shortcut factor for creating a Float64 metric
func FloatMetric(name string, value float64, keyvalues ...string) Metric {
	return genericMetricFactory(name, Value{Float64: &value}, keyvalues...)
}

// AssertMetric verifies that the metrics have the specified values.
func AssertMetric(t *testing.T, values ...Metric) {
	t.Helper()
	for _, v := range values {
		if diff := cmp.Diff(v, GetOneMetric(v.Name)); diff != "" {
			t.Errorf("Wrong adds (-want +got): %s", diff)
		}
	}
}

// AssertMetricExists verifies that at least one metric values has been reported for
// each of metric names.
func AssertMetricExists(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if len(GetMetric(name)) == 0 {
			t.Errorf("No metrics found for %q", name)
		}
	}
}

// AssertNoMetric verifies that no metrics have been reported for any of the
// metric names.
func AssertNoMetric(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if m := GetMetric(name); len(m) != 0 {
			t.Error("Found unexpected data for:", m)
		}
	}
}

// VisitFloat64Value implements metricdata.ValueVisitor.
func (v *Value) VisitFloat64Value(f float64) {
	v.Float64 = &f
	v.Int64 = nil
	v.Distribution = nil
}

// VisitInt64Value implements metricdata.ValueVisitor.
func (v *Value) VisitInt64Value(i int64) {
	v.Int64 = &i
	v.Float64 = nil
	v.Distribution = nil
}

// VisitDistributionValue implements metricdata.ValueVisitor.
func (v *Value) VisitDistributionValue(d *metricdata.Distribution) {
	v.Distribution = d
	v.Int64 = nil
	v.Float64 = nil
}

// VisitSummaryValue implements metricdata.ValueVisitor.
func (v *Value) VisitSummaryValue(*metricdata.Summary) {
	panic("Attempted to fetch summary value, which we never use!")
}

// Equal provides a contract for use with github.com/google/go-cmp/cmp. Due to
// the reflection in cmp, it only works if the type of the two arguments to cmp
// are the same.
func (m Metric) Equal(other Metric) bool {
	if m.Name != other.Name {
		return false
	}
	if (m.Unit != "" || m.VerifyMetadata) && (other.Unit != "" || other.VerifyMetadata) {
		if m.Unit != other.Unit {
			return false
		}
	}
	if m.VerifyMetadata && other.VerifyMetadata {
		if m.Type != other.Type {
			return false
		}
	}

	if (m.Resource != nil || m.VerifyResource) && (other.Resource != nil || other.VerifyResource) {
		if !cmp.Equal(m.Resource, other.Resource) {
			return false
		}
	}

	if len(m.Values) > 0 && len(other.Values) > 0 {
		if len(m.Values) != len(other.Values) {
			return false
		}
		myValues := make(map[string]Value, len(m.Values))
		for _, v := range m.Values {
			myValues[tagsToString(v.Tags)] = v
		}
		for _, v := range other.Values {
			myV, ok := myValues[tagsToString(v.Tags)]
			if !ok || !myV.Equal(v) {
				return false
			}
		}
	}

	return true
}

// Equal provides a contract for github.com/google/go-cmp/cmp. It compares two
// values, including deep comparison of Distributions. (Exemplars are
// intentional not included in the comparison, but other fields are considered).
func (v Value) Equal(other Value) bool {
	if len(v.Tags) != len(other.Tags) {
		return false
	}
	for k, v := range v.Tags {
		if v != other.Tags[k] {
			return false
		}
	}
	if v.Int64 != nil {
		return other.Int64 != nil && *v.Int64 == *other.Int64
	}
	if v.Float64 != nil {
		return other.Float64 != nil && *v.Float64 == *other.Float64
	}

	if v.Distribution != nil {
		if other.Distribution == nil {
			return false
		}
		if v.Distribution.Count != other.Distribution.Count {
			return false
		}
		if v.Distribution.Sum != other.Distribution.Sum {
			return false
		}
		if v.Distribution.SumOfSquaredDeviation != other.Distribution.SumOfSquaredDeviation {
			return false
		}
		if v.Distribution.BucketOptions != nil {
			if other.Distribution.BucketOptions == nil {
				return false
			}
			for i, bo := range v.Distribution.BucketOptions.Bounds {
				if bo != other.Distribution.BucketOptions.Bounds[i] {
					return false
				}
			}
		}
		for i, b := range v.Distribution.Buckets {
			if b.Count != other.Distribution.Buckets[i].Count {
				return false
			}
		}
	}

	return true
}

func tagsToString(tags map[string]string) string {
	pairs := make([]string, 0, len(tags))
	for k, v := range tags {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ",")
}
