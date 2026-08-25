package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMetadataValue_SetAndReadTypedValue(t *testing.T) {
	testCases := []struct {
		name      string
		valueType MetadataValueType
		input     any
		expected  any
	}{
		{name: "text", valueType: MetadataValueTypeText, input: "release notes", expected: "release notes"},
		{name: "single select", valueType: MetadataValueTypeSingleSelect, input: "option-a", expected: "option-a"},
		{
			name:      "multi select",
			valueType: MetadataValueTypeMultiSelect,
			input:     []any{"option-b", "option-a"},
			expected:  []string{"option-b", "option-a"},
		},
		{name: "number", valueType: MetadataValueTypeNumber, input: 42.5, expected: 42.5},
		{name: "date", valueType: MetadataValueTypeDate, input: "2026-08-21", expected: "2026-08-21"},
		{name: "boolean false", valueType: MetadataValueTypeBoolean, input: false, expected: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			value := &MetadataValue{}
			require.NoError(t, value.SetTypedValue(testCase.valueType, testCase.input))
			require.Equal(t, testCase.expected, value.TypedValue(testCase.valueType))
			require.True(t, value.HasValue(testCase.valueType))

			require.NoError(t, value.SetTypedValue(testCase.valueType, nil))
			require.Nil(t, value.TypedValue(testCase.valueType))
			require.False(t, value.HasValue(testCase.valueType))
		})
	}
}

func TestMetadataValue_SetTypedValueRejectsWrongShape(t *testing.T) {
	testCases := []struct {
		valueType MetadataValueType
		input     any
	}{
		{valueType: MetadataValueTypeText, input: 123},
		{valueType: MetadataValueTypeSingleSelect, input: []string{"option-a"}},
		{valueType: MetadataValueTypeMultiSelect, input: []any{"option-a", 2}},
		{valueType: MetadataValueTypeNumber, input: "42"},
		{valueType: MetadataValueTypeDate, input: "2026-02-30"},
		{valueType: MetadataValueTypeBoolean, input: "true"},
		{valueType: MetadataValueType("unsupported"), input: "value"},
	}

	for _, testCase := range testCases {
		value := &MetadataValue{}
		require.ErrorIs(t, value.SetTypedValue(testCase.valueType, testCase.input), ErrInvalidMetadataValue)
		require.False(t, value.HasAnyScalarValue())
		require.Empty(t, value.OptionIDs)
	}
}
