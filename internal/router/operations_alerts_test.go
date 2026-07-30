package router

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

type recordedOperationsEmail struct {
	subject string
	body    string
}

type recordingOperationsEmailSender struct {
	sent []recordedOperationsEmail
	err  error
}

func (s *recordingOperationsEmailSender) Send(_ context.Context, subject, body string) error {
	s.sent = append(s.sent, recordedOperationsEmail{subject: subject, body: body})
	return s.err
}

func TestOperationsEmailAlerterDeduplicatesAndSendsRecovery(t *testing.T) {
	sender := &recordingOperationsEmailSender{}
	alerter := newOperationsEmailAlerter(operationsEmailAlertConfig{
		Cooldown: time.Minute,
		Timeout:  time.Second,
	}, sender)
	started := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)
	condition := operationsAlertCondition{
		Key: "database_unavailable", Severity: "critical", Summary: "database dependency unavailable",
	}

	alerter.process(started, []operationsAlertCondition{condition})
	alerter.process(started.Add(30*time.Second), []operationsAlertCondition{condition})
	if len(sender.sent) != 1 {
		t.Fatalf("notifications after duplicate failure = %d, want 1", len(sender.sent))
	}
	if !strings.Contains(sender.sent[0].subject, "critical") || !strings.Contains(sender.sent[0].body, "credentials and raw dependency errors") {
		t.Fatalf("unexpected failure email: %#v", sender.sent[0])
	}

	alerter.process(started.Add(31*time.Second), nil)
	if len(sender.sent) != 2 {
		t.Fatalf("notifications after recovery = %d, want 2", len(sender.sent))
	}
	if !strings.Contains(sender.sent[1].subject, "recovery") {
		t.Fatalf("recovery email subject = %q", sender.sent[1].subject)
	}
}

func TestOperationsEmailAlerterThrottlesDeliveryFailureLogs(t *testing.T) {
	sender := &recordingOperationsEmailSender{err: errors.New("smtp unavailable")}
	alerter := newOperationsEmailAlerter(operationsEmailAlertConfig{
		Cooldown: time.Minute,
		Timeout:  time.Second,
	}, sender)
	logged := 0
	alerter.onDeliveryError = func(string) { logged++ }
	started := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)
	condition := operationsAlertCondition{
		Key: "database_unavailable", Severity: "critical", Summary: "database dependency unavailable",
	}

	alerter.process(started, []operationsAlertCondition{condition})
	alerter.process(started.Add(30*time.Second), []operationsAlertCondition{condition})
	alerter.process(started.Add(time.Minute), []operationsAlertCondition{condition})

	if len(sender.sent) != 2 {
		t.Fatalf("send attempts = %d, want 2", len(sender.sent))
	}
	if logged != 2 {
		t.Fatalf("delivery failure logs = %d, want 2", logged)
	}

	alerter.process(started.Add(time.Minute+time.Second), nil)
	if len(sender.sent) != 2 {
		t.Fatalf("unexpected recovery email after undelivered failure: %#v", sender.sent)
	}
}

func TestLoadOperationsEmailAlertConfigFromEnv(t *testing.T) {
	t.Setenv("OPS_ALERT_EMAIL_ENABLED", "true")
	t.Setenv("OPS_ALERT_EMAIL_TO", "operator@example.com, secondary@example.com,operator@example.com")
	t.Setenv("SMTP_FROM", "WeKnora Alerts <alerts@example.com>")
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_PORT", "465")
	t.Setenv("SMTP_TLS_MODE", "implicit")
	t.Setenv("SMTP_USERNAME", "alert-user")
	t.Setenv("SMTP_PASSWORD", "test-secret")
	t.Setenv("SMTP_PASSWORD_FILE", "")
	t.Setenv("OPS_ALERT_CHECK_INTERVAL_SECONDS", "120")
	t.Setenv("OPS_ALERT_COOLDOWN_SECONDS", "300")
	t.Setenv("OPS_ALERT_TIMEOUT_SECONDS", "8")

	config, warnings := loadOperationsEmailAlertConfigFromEnv()
	if len(warnings) != 0 {
		t.Fatalf("unexpected config warnings: %v", warnings)
	}
	if !config.Enabled || config.Port != 465 || config.TLSMode != smtpTLSImplicit || len(config.To) != 2 || config.From != "alerts@example.com" || config.CheckInterval != 2*time.Minute || config.Timeout != 8*time.Second {
		t.Fatalf("unexpected config: %#v", config)
	}
}

func TestLoadOperationsEmailAlertConfigRejectsInsecureAuthentication(t *testing.T) {
	t.Setenv("OPS_ALERT_EMAIL_ENABLED", "true")
	t.Setenv("OPS_ALERT_EMAIL_TO", "operator@example.com")
	t.Setenv("SMTP_FROM", "alerts@example.com")
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_TLS_MODE", "none")
	t.Setenv("SMTP_USERNAME", "alert-user")
	t.Setenv("SMTP_PASSWORD", "test-secret")
	t.Setenv("SMTP_PASSWORD_FILE", "")

	config, warnings := loadOperationsEmailAlertConfigFromEnv()
	if config.Enabled || len(warnings) == 0 || !strings.Contains(strings.Join(warnings, " "), "cannot be used with SMTP authentication") {
		t.Fatalf("unexpected insecure SMTP configuration result: %#v warnings=%v", config, warnings)
	}
}

func TestSMTPOperationsEmailSenderDeliversMessage(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen returned error: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	messages := make(chan string, 1)
	go serveTestSMTP(t, listener, messages)

	port := listener.Addr().(*net.TCPAddr).Port
	sender := smtpOperationsEmailSender{config: operationsEmailAlertConfig{
		To:      []string{"operator@example.com", "secondary@example.com"},
		From:    "alerts@example.com",
		Host:    "127.0.0.1",
		Port:    port,
		TLSMode: smtpTLSNone,
	}}
	if err := sender.Send(context.Background(), "WeKnora critical alert", "database dependency unavailable\r\n"); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	select {
	case message := <-messages:
		if !strings.Contains(message, "To: operator@example.com, secondary@example.com") || !strings.Contains(message, "Subject: WeKnora critical alert") || !strings.Contains(message, "database dependency unavailable") {
			t.Fatalf("unexpected SMTP message: %q", message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SMTP test server did not receive a message")
	}
}

func serveTestSMTP(t *testing.T, listener net.Listener, messages chan<- string) {
	t.Helper()
	connection, err := listener.Accept()
	if err != nil {
		return
	}
	defer connection.Close()

	reader := bufio.NewReader(connection)
	writer := bufio.NewWriter(connection)
	writeResponse := func(response string) {
		_, _ = writer.WriteString(response + "\r\n")
		_ = writer.Flush()
	}
	writeResponse("220 test-smtp")

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(command, "EHLO ") || strings.HasPrefix(command, "HELO "):
			writeResponse("250 test-smtp")
		case strings.HasPrefix(command, "MAIL FROM:"), strings.HasPrefix(command, "RCPT TO:"):
			writeResponse("250 accepted")
		case command == "DATA":
			writeResponse("354 end data with <CR><LF>.<CR><LF>")
			var content strings.Builder
			for {
				dataLine, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimSpace(dataLine) == "." {
					break
				}
				content.WriteString(dataLine)
			}
			messages <- content.String()
			writeResponse("250 queued")
		case command == "QUIT":
			writeResponse("221 bye")
			return
		default:
			t.Errorf("unexpected SMTP command: %q", command)
			writeResponse("500 unsupported")
			return
		}
	}
}
