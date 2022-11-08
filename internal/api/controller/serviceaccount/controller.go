// Copyright 2022 Harness Inc. All rights reserved.
// Use of this source code is governed by the Polyform Free Trial License
// that can be found in the LICENSE.md file for this repository.

package serviceaccount

import (
	"github.com/harness/gitness/internal/auth/authz"
	"github.com/harness/gitness/internal/store"
	"github.com/harness/gitness/types/check"
)

type Controller struct {
	serviceAccountCheck check.ServiceAccount
	authorizer          authz.Authorizer
	saStore             store.ServiceAccountStore
	spaceStore          store.SpaceStore
	repoStore           store.RepoStore
	tokenStore          store.TokenStore
}

func NewController(serviceAccountCheck check.ServiceAccount, authorizer authz.Authorizer,
	saStore store.ServiceAccountStore, spaceStore store.SpaceStore, repoStore store.RepoStore,
	tokenStore store.TokenStore) *Controller {
	return &Controller{
		serviceAccountCheck: serviceAccountCheck,
		authorizer:          authorizer,
		saStore:             saStore,
		spaceStore:          spaceStore,
		repoStore:           repoStore,
		tokenStore:          tokenStore,
	}
}