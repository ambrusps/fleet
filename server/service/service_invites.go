package service

import (
	"context"
	"encoding/base64"
	"html/template"

	"github.com/fleetdm/fleet/server/contexts/viewer"
	"github.com/fleetdm/fleet/server/kolide"
	"github.com/fleetdm/fleet/server/mail"
	"github.com/pkg/errors"
)

func (svc Service) InviteNewUser(ctx context.Context, payload kolide.InvitePayload) (*kolide.Invite, error) {
	if err := svc.authz.Authorize(ctx, &kolide.Invite{}, kolide.ActionWrite); err != nil {
		return nil, err
	}

	// verify that the user with the given email does not already exist
	_, err := svc.ds.UserByEmail(*payload.Email)
	if err == nil {
		return nil, kolide.NewInvalidArgumentError("email", "a user with this account already exists")
	}
	if _, ok := err.(kolide.NotFoundError); !ok {
		return nil, err
	}

	// find the user who created the invite
	v, ok := viewer.FromContext(ctx)
	if !ok {
		return nil, errors.New("missing viewer context for create invite")
	}
	inviter := v.User

	random, err := kolide.RandomText(svc.config.App.TokenKeySize)
	if err != nil {
		return nil, err
	}
	token := base64.URLEncoding.EncodeToString([]byte(random))

	invite := &kolide.Invite{
		Email:      *payload.Email,
		InvitedBy:  inviter.ID,
		Token:      token,
		GlobalRole: payload.GlobalRole,
		Teams:      payload.Teams,
	}
	if payload.Position != nil {
		invite.Position = *payload.Position
	}
	if payload.Name != nil {
		invite.Name = *payload.Name
	}
	if payload.SSOEnabled != nil {
		invite.SSOEnabled = *payload.SSOEnabled
	}

	invite, err = svc.ds.NewInvite(invite)
	if err != nil {
		return nil, err
	}

	config, err := svc.AppConfig(ctx)
	if err != nil {
		return nil, err
	}

	invitedBy := inviter.Name
	if invitedBy == "" {
		invitedBy = inviter.Username
	}
	inviteEmail := kolide.Email{
		Subject: "You are Invited to Fleet",
		To:      []string{invite.Email},
		Config:  config,
		Mailer: &mail.InviteMailer{
			Invite:            invite,
			BaseURL:           template.URL(config.KolideServerURL + svc.config.Server.URLPrefix),
			AssetURL:          getAssetURL(),
			OrgName:           config.OrgName,
			InvitedByUsername: invitedBy,
		},
	}

	err = svc.mailService.SendEmail(inviteEmail)
	if err != nil {
		return nil, err
	}
	return invite, nil
}

func (svc *Service) ListInvites(ctx context.Context, opt kolide.ListOptions) ([]*kolide.Invite, error) {
	if err := svc.authz.Authorize(ctx, &kolide.Invite{}, kolide.ActionRead); err != nil {
		return nil, err
	}
	return svc.ds.ListInvites(opt)
}

func (svc *Service) VerifyInvite(ctx context.Context, token string) (*kolide.Invite, error) {
	// skipauth: There is no viewer context at this point. We rely on verifying
	// the invite for authNZ.
	svc.authz.SkipAuthorization(ctx)

	invite, err := svc.ds.InviteByToken(token)
	if err != nil {
		return nil, err
	}

	if invite.Token != token {
		return nil, kolide.NewInvalidArgumentError("invite_token", "Invite Token does not match Email Address.")
	}

	expiresAt := invite.CreatedAt.Add(svc.config.App.InviteTokenValidityPeriod)
	if svc.clock.Now().After(expiresAt) {
		return nil, kolide.NewInvalidArgumentError("invite_token", "Invite token has expired.")
	}

	return invite, nil

}

func (svc *Service) DeleteInvite(ctx context.Context, id uint) error {
	if err := svc.authz.Authorize(ctx, &kolide.Invite{}, kolide.ActionWrite); err != nil {
		return err
	}
	return svc.ds.DeleteInvite(id)
}
