package yunzhijia

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"strings"
)

// computeSignature builds the signature string from the callback fields
// and returns the Base64(HMAC_SHA1(secret, signatureString)).
//
// The signature string is formed by joining the following fields in order
// with commas: robotId, robotName, operatorOpenid, operatorName, time, msgId, content.
func computeSignature(secret string, msg *callbackMessage) string {
	// Build signature string: field1,field2,...,field7
	parts := []string{
		msg.RobotID,
		msg.RobotName,
		msg.OperatorOpenid,
		msg.OperatorName,
		fmt.Sprintf("%d", msg.Time),
		msg.MsgID,
		msg.Content,
	}
	signatureString := strings.Join(parts, ",")

	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(signatureString))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
