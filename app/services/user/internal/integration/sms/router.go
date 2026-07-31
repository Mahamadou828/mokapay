package sms

import "context"

// Router splits delivery by destination: numbers matching isTest go to the
// test sender, everything else to live.
//
// It exists for the seeded test accounts (auth.IsTestPhone). Their OTP is a
// fixed, publicly-known code, so a real send would spend credit delivering
// something the team already knows — and the numbers are not guaranteed to
// belong to a real handset. Routing them to the logging sender keeps them
// working in every environment, production included.
type Router struct {
	live   Sender
	test   Sender
	isTest func(phoneNumber string) bool
}

// NewRouter wires the two senders. isTest is required — a nil predicate is a
// wiring bug and will panic on the first send rather than silently billing
// real sends for test traffic.
func NewRouter(live, test Sender, isTest func(phoneNumber string) bool) Router {
	return Router{live: live, test: test, isTest: isTest}
}

// Send implements Sender.
func (r Router) Send(ctx context.Context, phoneNumber, message string) error {
	if r.isTest(phoneNumber) {
		return r.test.Send(ctx, phoneNumber, message)
	}
	return r.live.Send(ctx, phoneNumber, message)
}
