package builtin_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/desensitization/builtin"
	"github.com/Tencent/WeKnora/internal/types"
)

func mask(t *testing.T, text string, typesEnabled []string, style string, rules ...types.DesensitizationRule) string {
	t.Helper()
	out, _, err := builtin.Mask(context.Background(), text, types.DesensitizationConfig{
		Enabled:     true,
		Engine:      types.DesensitizationEngineBuiltin,
		MaskStyle:   style,
		EntityTypes: typesEnabled,
		Rules:       rules,
	})
	require.NoError(t, err)
	return out
}

func TestBuiltinLeavesTextUnchangedWhenDisabled(t *testing.T) {
	id := mustCNID("11010119900307851")
	in := "证件 " + id
	out, _, err := builtin.Mask(context.Background(), in, types.DesensitizationConfig{Enabled: false})
	require.NoError(t, err)
	assert.Equal(t, in, out)
}

func TestBuiltinMasksValidChineseIDCard(t *testing.T) {
	id := mustCNID("11010119900307851")
	in := "联系人证件号" + id + "已登记"
	out := mask(t, in, []string{types.DesensitizationEntityCNIDCard}, types.DesensitizationMaskReplace)
	assert.NotContains(t, out, id)
	assert.Contains(t, out, "<身份证>")
}

func TestBuiltinRejectsIDCardWithBadChecksum(t *testing.T) {
	in := "证件号110101199003078511已登记"
	out := mask(t, in, []string{types.DesensitizationEntityCNIDCard}, types.DesensitizationMaskReplace)
	assert.Contains(t, out, "110101199003078511")
	assert.NotContains(t, out, "<身份证>")
}

func TestBuiltinMasksIDCardWithSpaces(t *testing.T) {
	id := mustCNID("11010119900307851")
	spaced := id[0:4] + " " + id[4:8] + " " + id[8:12] + " " + id[12:16] + " " + id[16:18]
	in := "证件 " + spaced + " 已登记"
	out := mask(t, in, []string{types.DesensitizationEntityCNIDCard}, types.DesensitizationMaskReplace)
	assert.NotContains(t, out, spaced)
	assert.Contains(t, out, "<身份证>")
}

func TestBuiltinRejectsIDCardUnknownProvinceOrFutureBirth(t *testing.T) {
	unknownProv := mustCNID("99010119900307851")
	future := mustCNID("11010120990101123")
	for _, id := range []string{unknownProv, future} {
		out := mask(
			t, "证件"+id+"已登记",
			[]string{types.DesensitizationEntityCNIDCard}, types.DesensitizationMaskReplace,
		)
		assert.Contains(t, out, id)
		assert.NotContains(t, out, "<身份证>")
	}
}

func TestBuiltinMasksMainlandMobile(t *testing.T) {
	in := "请拨打13800138000联系"
	out := mask(t, in, []string{types.DesensitizationEntityCNMobile}, types.DesensitizationMaskPartial)
	assert.NotContains(t, out, "13800138000")
	assert.Contains(t, out, "138****8000")
}

func TestBuiltinDoesNotTreatLongDigitsAsMobile(t *testing.T) {
	in := "流水号13800138000123"
	out := mask(t, in, []string{types.DesensitizationEntityCNMobile}, types.DesensitizationMaskReplace)
	assert.Contains(t, out, "13800138000123")
}

func TestBuiltinMasksMobileWithCountryCodeAndSeparators(t *testing.T) {
	cases := []string{
		"+8613800138000",
		"+86 13800138000",
		"008613800138000",
		"0086 13800138000",
		"138-1234-5678",
		"138 1234 5678",
		"+86 138-1234-5678",
	}
	for _, raw := range cases {
		in := "请拨打" + raw + "联系"
		out := mask(t, in, []string{types.DesensitizationEntityCNMobile}, types.DesensitizationMaskReplace)
		assert.NotContains(t, out, raw, "input %q", raw)
		assert.Contains(t, out, "<手机号>", "input %q", raw)
	}

	entities := []string{types.DesensitizationEntityCNMobile}
	partial := mask(t, "请拨打+86 138-1234-5678联系", entities, types.DesensitizationMaskPartial)
	assert.Contains(t, partial, "+86")
	assert.Contains(t, partial, "138")
	assert.Contains(t, partial, "5678")
	assert.NotContains(t, partial, "1234")
}

func TestBuiltinKeepsMobileMIITSegments(t *testing.T) {
	// 144 is outside 14[5-9]; 160 is outside 16[2567]. Do not widen to 1[3-9].
	for _, raw := range []string{"14400144000", "16012345678"} {
		out := mask(
			t, "号码"+raw+"结束",
			[]string{types.DesensitizationEntityCNMobile}, types.DesensitizationMaskReplace,
		)
		assert.Contains(t, out, raw)
		assert.NotContains(t, out, "<手机号>")
	}
}

func TestBuiltinMasksLandline(t *testing.T) {
	in := "总机010-12345678转分机"
	out := mask(t, in, []string{types.DesensitizationEntityCNLandline}, types.DesensitizationMaskReplace)
	assert.NotContains(t, out, "010-12345678")
	assert.Contains(t, out, "<固定电话>")
}

func TestBuiltinMasksUnionPayCardWithLuhn(t *testing.T) {
	card := mustLuhn("622202123456789")
	in := "卡号" + card + "已绑定"
	out := mask(t, in, []string{types.DesensitizationEntityCNBankCard}, types.DesensitizationMaskReplace)
	assert.NotContains(t, out, card)
	assert.Contains(t, out, "<银行卡>")
}

func TestBuiltinMasksUnionPayCardWithSpacesAndHyphens(t *testing.T) {
	card := mustLuhn("622202001234567")
	require.Len(t, card, 16)
	spaced := card[0:4] + " " + card[4:8] + " " + card[8:12] + " " + card[12:16]
	hyphen := card[0:4] + "-" + card[4:8] + "-" + card[8:12] + "-" + card[12:16]
	mixed := card[0:4] + " " + card[4:8] + "-" + card[8:12] + " " + card[12:16]

	for _, formatted := range []string{spaced, hyphen, mixed} {
		in := "卡号" + formatted + "已绑定"
		out := mask(t, in, []string{types.DesensitizationEntityCNBankCard}, types.DesensitizationMaskReplace)
		assert.NotContains(t, out, formatted, "input %q", formatted)
		assert.Contains(t, out, "<银行卡>", "input %q", formatted)
	}

	partial := mask(
		t, "卡号"+spaced+"已绑定",
		[]string{types.DesensitizationEntityCNBankCard},
		types.DesensitizationMaskPartial,
	)
	assert.NotContains(t, partial, card)
	assert.Contains(t, partial, card[:4])
	assert.Contains(t, partial, card[len(card)-4:])
	assert.Contains(t, partial, " ")
}

func TestBuiltinMasksUnionPayCardWithConsecutiveSpaces(t *testing.T) {
	card := mustLuhn("622202001234567")
	require.Len(t, card, 16)
	spaced := card[0:4] + "  " + card[4:8] + "  " + card[8:12] + "  " + card[12:16]
	out := mask(
		t, "卡号"+spaced+"已绑定",
		[]string{types.DesensitizationEntityCNBankCard}, types.DesensitizationMaskReplace,
	)
	assert.NotContains(t, out, spaced)
	assert.Contains(t, out, "<银行卡>")
}

func TestBuiltinDoesNotMaskNonUnionPayLuhnCard(t *testing.T) {
	// Visa test PAN 4111… passes Luhn but is not UnionPay 62.
	visa := "4111111111111111"
	out := mask(
		t, "卡号"+visa+"已绑定",
		[]string{types.DesensitizationEntityCNBankCard}, types.DesensitizationMaskReplace,
	)
	assert.Contains(t, out, visa)
	assert.NotContains(t, out, "<银行卡>")
}

func TestBuiltinLeavesFormattedCardIfLuhnFails(t *testing.T) {
	good := mustLuhn("622202001234567")
	require.Len(t, good, 16)
	badDigit := byte('0' + (int(good[15]-'0')+1)%10)
	bad := good[:15] + string(badDigit)
	formatted := bad[0:4] + " " + bad[4:8] + " " + bad[8:12] + " " + bad[12:16]
	out := mask(
		t, "卡号"+formatted+"已绑定",
		[]string{types.DesensitizationEntityCNBankCard},
		types.DesensitizationMaskReplace,
	)
	assert.Contains(t, out, formatted)
	assert.NotContains(t, out, "<银行卡>")
}

func TestBuiltinPrefersIDCardOverBankWhenBothCouldMatch(t *testing.T) {
	id := mustCNID("11010119900307851")
	in := "号码" + id
	out := mask(t, in, []string{
		types.DesensitizationEntityCNIDCard,
		types.DesensitizationEntityCNBankCard,
	}, types.DesensitizationMaskReplace)
	assert.Contains(t, out, "<身份证>")
	assert.NotContains(t, out, "<银行卡>")
}

func TestBuiltinMasksUSCC(t *testing.T) {
	code := mustUSCC("91110000MA0123456")
	in := "主体代码" + code
	out := mask(t, in, []string{types.DesensitizationEntityCNUSCC}, types.DesensitizationMaskReplace)
	assert.NotContains(t, out, code)
	assert.Contains(t, out, "<统一社会信用代码>")
}

func TestBuiltinMasksPlateAndEmail(t *testing.T) {
	in := "车辆京A12345，邮箱user@example.com"
	out := mask(t, in, []string{
		types.DesensitizationEntityCNPlate, types.DesensitizationEntityEmail,
	}, types.DesensitizationMaskReplace)
	assert.NotContains(t, out, "京A12345")
	assert.NotContains(t, out, "user@example.com")
	assert.Contains(t, out, "<车牌号>")
	assert.Contains(t, out, "<邮箱>")
}

func TestBuiltinRejectsMalformedEmails(t *testing.T) {
	for _, raw := range []string{"test@.com", "test@example..com", ".user@example.com", "user.@example.com"} {
		out := mask(
			t, "邮箱"+raw+"结束",
			[]string{types.DesensitizationEntityEmail}, types.DesensitizationMaskReplace,
		)
		assert.Contains(t, out, raw)
		assert.NotContains(t, out, "<邮箱>")
	}
}

func TestBuiltinPartialEmailMasksDomain(t *testing.T) {
	out := mask(
		t, "邮箱user@example.com结束",
		[]string{types.DesensitizationEntityEmail}, types.DesensitizationMaskPartial,
	)
	assert.NotContains(t, out, "user@example.com")
	assert.NotContains(t, out, "example.com")
	assert.Contains(t, out, "us")
	assert.Contains(t, out, "@")
	assert.Contains(t, out, strings.Repeat("*", len("example.com")))
}

func TestBuiltinAppliesCustomRegexp(t *testing.T) {
	in := "工单 SECRET-9981 已关闭"
	out := mask(t, in, nil, types.DesensitizationMaskReplace, types.DesensitizationRule{
		Name:        "工单号",
		Pattern:     `SECRET-\d+`,
		Replacement: "<工单号>",
	})
	assert.Equal(t, "工单 <工单号> 已关闭", out)
}

func TestBuiltinRejectsInvalidCustomRegexp(t *testing.T) {
	_, _, err := builtin.Mask(context.Background(), "abc", types.DesensitizationConfig{
		Enabled: true,
		Rules:   []types.DesensitizationRule{{Name: "bad", Pattern: "("}},
	})
	require.Error(t, err)
}

func TestBuiltinValidateRules(t *testing.T) {
	require.NoError(t, builtin.ValidateRules(nil))
	require.NoError(t, builtin.ValidateRules([]types.DesensitizationRule{{Name: "ok", Pattern: `SECRET-\d+`}}))
	require.Error(t, builtin.ValidateRules([]types.DesensitizationRule{{Name: "bad", Pattern: "("}}))
}

func mustCNID(body17 string) string {
	if utf8.RuneCountInString(body17) != 17 {
		panic("id body must be 17 digits")
	}
	weights := []int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}
	codes := []byte{'1', '0', 'X', '9', '8', '7', '6', '5', '4', '3', '2'}
	sum := 0
	for i, w := range weights {
		sum += int(body17[i]-'0') * w
	}
	return body17 + string(codes[sum%11])
}

func mustLuhn(bodyWithoutCheck string) string {
	// Append a check digit so the whole number passes Luhn.
	payload := bodyWithoutCheck + "0"
	sum := luhnSum(payload)
	check := (10 - (sum % 10)) % 10
	return bodyWithoutCheck + strconv.Itoa(check)
}

func luhnSum(num string) int {
	sum := 0
	alt := false
	for i := len(num) - 1; i >= 0; i-- {
		n := int(num[i] - '0')
		if alt {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		alt = !alt
	}
	return sum
}

func mustUSCC(body17 string) string {
	const charset = "0123456789ABCDEFGHJKLMNPQRTUWXY"
	index := map[byte]int{}
	for i := 0; i < len(charset); i++ {
		index[charset[i]] = i
	}
	weights := []int{1, 3, 9, 27, 19, 26, 16, 17, 20, 29, 25, 13, 8, 24, 10, 30, 28}
	sum := 0
	for i, w := range weights {
		sum += index[body17[i]] * w
	}
	check := charset[(31-(sum%31))%31]
	return body17 + string(check)
}

func TestMustCNIDHasValidBirthDate(t *testing.T) {
	id := mustCNID("11010119900307851")
	_, err := time.Parse("20060102", id[6:14])
	require.NoError(t, err)
	require.Len(t, id, 18)
}
