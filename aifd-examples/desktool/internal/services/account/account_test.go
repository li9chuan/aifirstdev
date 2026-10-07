package account

import "testing"

func TestLoginReturnsDemoAccountWithoutToken(t *testing.T) {
	service := NewService()
	login, err := service.Login(LoginRequest{Username: "demo", Password: "demo123"})
	if err != nil {
		t.Fatalf("Login error = %v", err)
	}
	if !login.OK || login.AccountID != DemoAccountID || login.UID != DemoUID {
		t.Fatalf("login = %+v, want demo account", login)
	}
}

func TestLoginRejectsInvalidCredentials(t *testing.T) {
	service := NewService()
	if result, err := service.Login(LoginRequest{Username: "demo", Password: "bad"}); err == nil || result.OK {
		t.Fatalf("Login result = %+v err=%v, want failure", result, err)
	}
}

func TestBindIdentity(t *testing.T) {
	service := NewService()
	result, err := service.BindIdentity(BindIdentityRequest{
		AccountID:  DemoAccountID,
		Provider:   "wechat",
		ExternalID: "openid-1",
	})
	if err != nil {
		t.Fatalf("BindIdentity error = %v", err)
	}
	if !result.OK || result.Identity.AccountID != DemoAccountID {
		t.Fatalf("BindIdentity result = %+v, want demo account", result)
	}
	again, err := service.BindIdentity(BindIdentityRequest{
		AccountID:  DemoAccountID,
		Provider:   "wechat",
		ExternalID: "openid-1",
	})
	if err != nil || !again.OK {
		t.Fatalf("BindIdentity repeat = %+v err=%v, want idempotent success", again, err)
	}
}

func TestBindIdentityRejectsConflict(t *testing.T) {
	service := NewService()
	service.accounts["acct-other"] = Account{AccountID: "acct-other", UID: 2002, Username: "other"}
	if _, err := service.BindIdentity(BindIdentityRequest{
		AccountID:  DemoAccountID,
		Provider:   "wechat",
		ExternalID: "openid-1",
	}); err != nil {
		t.Fatalf("first BindIdentity error = %v", err)
	}
	if result, err := service.BindIdentity(BindIdentityRequest{
		AccountID:  "acct-other",
		Provider:   "wechat",
		ExternalID: "openid-1",
	}); err == nil || result.OK {
		t.Fatalf("conflict result = %+v err=%v, want failure", result, err)
	}
}
