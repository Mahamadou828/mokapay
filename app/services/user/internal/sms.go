package internal

import (
	"context"
	"fmt"

	"mokapay.app/app/services/user/internal/business/auth"
	authUC "mokapay.app/app/services/user/internal/business/auth/usecase/auth"
	"mokapay.app/app/services/user/internal/integration/sms"
	"mokapay.app/core/logger"
)

// newSMSSender builds the OTP delivery sender SMS_PROVIDER asks for: real
// delivery once that provider's credentials are configured, logging stand-in
// without them (local dev needs none). With a provider wired, the seeded test
// phones are still routed to the logging sender — their code is the fixed
// auth.TestOtpCode, so a real send would only burn credit.
//
// An unknown provider is an error rather than a silent fallback: in
// production that would deliver nothing and leak every OTP code into the
// logs.
func newSMSSender(ctx context.Context, log *logger.Logger, cfg Config) (authUC.OTPSender, error) {
	logSMS := sms.NewLog(log)

	switch provider := sms.Provider(cfg.SMS.Provider); provider {
	case sms.ProviderAfricasTalking:
		if cfg.SMS.ATUsername == "" || cfg.SMS.ATAPIKey == "" {
			log.Info(ctx, "startup", "status", "no sms credentials, otp codes are logged only")
			return logSMS, nil
		}

		sender := sms.NewRouter(
			sms.NewAfricasTalking(log, cfg.SMS.ATUsername, cfg.SMS.ATAPIKey, cfg.SMS.ATSenderID),
			logSMS,
			auth.IsTestPhone,
		)
		log.Info(ctx, "startup", "status", "africa's talking sms sender enabled", "sender_id", cfg.SMS.ATSenderID)
		return sender, nil

	case sms.ProviderTwilio:
		if cfg.SMS.TwilioAccountSID == "" || cfg.SMS.TwilioAuthToken == "" {
			log.Info(ctx, "startup", "status", "no twilio credentials, otp codes are logged only")
			return logSMS, nil
		}

		twilio, err := sms.NewTwilio(log, cfg.SMS.TwilioAccountSID, cfg.SMS.TwilioAuthToken, cfg.SMS.TwilioFrom, cfg.SMS.TwilioMessagingServiceSID)
		if err != nil {
			return nil, fmt.Errorf("building twilio sender: %w", err)
		}

		sender := sms.NewRouter(twilio, logSMS, auth.IsTestPhone)
		log.Info(ctx, "startup", "status", "twilio sms sender enabled", "from", cfg.SMS.TwilioFrom, "messaging_service_sid", cfg.SMS.TwilioMessagingServiceSID)
		return sender, nil

	default:
		return nil, fmt.Errorf("unknown SMS_PROVIDER %q", provider)
	}
}
