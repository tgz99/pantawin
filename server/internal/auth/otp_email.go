package auth

import "fmt"

// RenderOTPEmail builds the verification email subject/body. Kept in this
// package (not notify) so auth has no dependency on the alert-email
// machinery; main.go supplies the actual SMTP transport via WithOTPMailer.
func RenderOTPEmail(code string) (subject, html string) {
	subject = "Your Pantawin verification code"
	html = fmt.Sprintf(`
<div style="font-family:-apple-system,Segoe UI,Roboto,sans-serif;max-width:480px;margin:0 auto">
  <table width="100%%" cellpadding="0" cellspacing="0" style="border-collapse:collapse">
    <tr><td style="background:#1a3a6e;color:#fff;padding:16px 20px;font-size:18px;font-weight:600;border-radius:8px 8px 0 0">
      Verify your Pantawin account
    </td></tr>
    <tr><td style="border:1px solid #eee;border-top:0;padding:24px;border-radius:0 0 8px 8px">
      <p style="margin:0 0 16px;color:#333">Enter this code in the app to finish creating your account:</p>
      <p style="margin:0 0 16px;font-size:32px;font-weight:700;letter-spacing:6px;color:#1a3a6e;text-align:center">%s</p>
      <p style="margin:0;color:#888;font-size:13px">This code expires in 10 minutes. If you didn't request it, you can ignore this email.</p>
    </td></tr>
  </table>
</div>`, code)
	return subject, html
}
