package utils

import (
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"time"

	l4g "github.com/alecthomas/log4go"
	"github.com/teddy/sign-in-on/model"
)

func encodeRFC2047Word(s string) string {
	return mime.BEncoding.Encode("utf-8", s)
}

func connectToSMTPServer(config *model.Config) (net.Conn, *model.AppError) {
	var conn net.Conn
	var err error

	if config.EmailSettings.ConnectionSecurity == model.CONN_SECURITY_TLS {
		tlsconfig := &tls.Config{
			InsecureSkipVerify: *config.EmailSettings.SkipServerCertificateVerification,
			ServerName:         config.EmailSettings.SMTPServer,
		}

		conn, err = tls.Dial("tcp", config.EmailSettings.SMTPServer+":"+config.EmailSettings.SMTPPort, tlsconfig)
		if err != nil {
			return nil, model.NewLocAppError("SendMail", "utils.mail.connect_smtp.open_tls.app_error", nil, err.Error())
		}
	} else {
		conn, err = net.Dial("tcp", config.EmailSettings.SMTPServer+":"+config.EmailSettings.SMTPPort)
		if err != nil {
			return nil, model.NewLocAppError("SendMail", "utils.mail.connect_smtp.open.app_error", nil, err.Error())
		}
	}

	return conn, nil
}

func newSMTPClient(conn net.Conn, config *model.Config) (*smtp.Client, *model.AppError) {
	c, err := smtp.NewClient(conn, config.EmailSettings.SMTPServer+":"+config.EmailSettings.SMTPPort)
	if err != nil {
		l4g.Error(T("utils.mail.new_client.open.error"), err)
		return nil, model.NewLocAppError("SendMail", "utils.mail.connect_smtp.open_tls.app_error", nil, err.Error())
	}

	hostname := GetHostnameFromSiteURL(*config.ServiceSettings.SiteURL)
	if hostname != "" {
		err := c.Hello(hostname)
		if err != nil {
			l4g.Error(T("utils.mail.new_client.helo.error"), err)
			return nil, model.NewLocAppError("SendMail", "utils.mail.connect_smtp.helo.app_error", nil, err.Error())
		}
	}

	if config.EmailSettings.ConnectionSecurity == model.CONN_SECURITY_STARTTLS {
		tlsconfig := &tls.Config{
			InsecureSkipVerify: *config.EmailSettings.SkipServerCertificateVerification,
			ServerName:         config.EmailSettings.SMTPServer,
		}
		c.StartTLS(tlsconfig)
	}

	if *config.EmailSettings.EnableSMTPAuth {
		auth := smtp.PlainAuth("", config.EmailSettings.SMTPUsername, config.EmailSettings.SMTPPassword, config.EmailSettings.SMTPServer+":"+config.EmailSettings.SMTPPort)

		if err = c.Auth(auth); err != nil {
			return nil, model.NewLocAppError("SendMail", "utils.mail.new_client.auth.app_error", nil, err.Error())
		}
	}
	return c, nil
}

func SendMail(to, subject, body string) *model.AppError {
	return SendMailUsingConfig(to, subject, body, Cfg)
}

func SendMailUsingConfig(to, subject, body string, config *model.Config) *model.AppError {
	if len(config.EmailSettings.SMTPServer) == 0 {
		return nil
	}

	l4g.Debug(T("utils.mail.send_mail.sending.debug"), to, subject)

	fromMail := mail.Address{Name: config.EmailSettings.FeedbackName, Address: config.EmailSettings.FeedbackEmail}
	toMail := mail.Address{Name: "", Address: to}

	headers := make(map[string]string)
	headers["From"] = fromMail.String()
	headers["To"] = toMail.String()
	headers["Subject"] = encodeRFC2047Word(subject)
	headers["MIME-version"] = "1.0"
	headers["Content-Type"] = "text/html; charset=\"utf-8\""
	headers["Content-Transfer-Encoding"] = "8bit"
	headers["Date"] = time.Now().Format(time.RFC1123Z)

	message := ""
	for k, v := range headers {
		message += fmt.Sprintf("%s: %s\r\n", k, v)
	}
	message += "\r\n<html><body>" + body + "</body></html>"

	conn, err1 := connectToSMTPServer(config)
	if err1 != nil {
		return err1
	}
	defer conn.Close()

	c, err2 := newSMTPClient(conn, config)
	if err2 != nil {
		return err2
	}
	defer c.Quit()
	defer c.Close()

	if err := c.Mail(fromMail.Address); err != nil {
		return model.NewLocAppError("SendMail", "utils.mail.send_mail.from_address.app_error", nil, err.Error())
	}

	if err := c.Rcpt(toMail.Address); err != nil {
		return model.NewLocAppError("SendMail", "utils.mail.send_mail.to_address.app_error", nil, err.Error())
	}

	w, err := c.Data()
	if err != nil {
		return model.NewLocAppError("SendMail", "utils.mail.send_mail.msg_data.app_error", nil, err.Error())
	}

	_, err = w.Write([]byte(message))
	if err != nil {
		return model.NewLocAppError("SendMail", "utils.mail.send_mail.msg.app_error", nil, err.Error())
	}

	err = w.Close()
	if err != nil {
		return model.NewLocAppError("SendMail", "utils.mail.send_mail.close.app_error", nil, err.Error())
	}

	return nil
}
