// Copyright 2022 Harness Inc. All rights reserved.
// Use of this source code is governed by the Polyform Free Trial License
// that can be found in the LICENSE.md file for this repository.

package database

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/harness/gitness/internal/store"
	"github.com/harness/gitness/store/database"
	"github.com/harness/gitness/store/database/dbtx"
	"github.com/harness/gitness/types"
	"github.com/harness/gitness/types/enum"

	"github.com/Masterminds/squirrel"
	"github.com/jmoiron/sqlx"
)

var _ store.MembershipStore = (*MembershipStore)(nil)

// NewMembershipStore returns a new MembershipStore.
func NewMembershipStore(db *sqlx.DB, pCache store.PrincipalInfoCache) *MembershipStore {
	return &MembershipStore{
		db:     db,
		pCache: pCache,
	}
}

// MembershipStore implements store.MembershipStore backed by a relational database.
type MembershipStore struct {
	db     *sqlx.DB
	pCache store.PrincipalInfoCache
}

type membership struct {
	SpaceID     int64 `db:"membership_space_id"`
	PrincipalID int64 `db:"membership_principal_id"`

	CreatedBy int64 `db:"membership_created_by"`
	Created   int64 `db:"membership_created"`
	Updated   int64 `db:"membership_updated"`

	Role enum.MembershipRole `db:"membership_role"`
}

type membershipPrincipal struct {
	membership
	principalInfo
}

type membershipSpace struct {
	membership
	space
}

const (
	membershipColumns = `
		 membership_space_id
		,membership_principal_id
		,membership_created_by
		,membership_created
		,membership_updated
		,membership_role`

	membershipSelectBase = `
	SELECT` + membershipColumns + `
	FROM memberships`
)

// Find finds the membership by space id and principal id.
func (s *MembershipStore) Find(ctx context.Context, key types.MembershipKey) (*types.Membership, error) {
	const sqlQuery = membershipSelectBase + `
	WHERE membership_space_id = $1 AND membership_principal_id = $2`

	db := dbtx.GetAccessor(ctx, s.db)

	dst := &membership{}
	if err := db.GetContext(ctx, dst, sqlQuery, key.SpaceID, key.PrincipalID); err != nil {
		return nil, database.ProcessSQLErrorf(err, "Failed to find membership")
	}

	result := mapToMembership(dst)

	return &result, nil
}

func (s *MembershipStore) FindUser(ctx context.Context, key types.MembershipKey) (*types.MembershipUser, error) {
	m, err := s.Find(ctx, key)
	if err != nil {
		return nil, err
	}

	result, err := s.addPrincipalInfos(ctx, m)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

// Create creates a new membership.
func (s *MembershipStore) Create(ctx context.Context, membership *types.Membership) error {
	const sqlQuery = `
	INSERT INTO memberships (
		 membership_space_id
		,membership_principal_id
		,membership_created_by
		,membership_created
		,membership_updated
		,membership_role
	) values (
		 :membership_space_id
		,:membership_principal_id
		,:membership_created_by
		,:membership_created
		,:membership_updated
		,:membership_role
	)`

	db := dbtx.GetAccessor(ctx, s.db)

	query, arg, err := db.BindNamed(sqlQuery, mapToInternalMembership(membership))
	if err != nil {
		return database.ProcessSQLErrorf(err, "Failed to bind membership object")
	}

	if _, err = db.ExecContext(ctx, query, arg...); err != nil {
		return database.ProcessSQLErrorf(err, "Failed to insert membership")
	}

	return nil
}

// Update updates the role of a member of a space.
func (s *MembershipStore) Update(ctx context.Context, membership *types.Membership) error {
	const sqlQuery = `
	UPDATE memberships
	SET
		 membership_updated = :membership_updated
		,membership_role = :membership_role
	WHERE membership_space_id = :membership_space_id AND
	      membership_principal_id = :membership_principal_id`

	db := dbtx.GetAccessor(ctx, s.db)

	dbMembership := mapToInternalMembership(membership)
	dbMembership.Updated = time.Now().UnixMilli()

	query, arg, err := db.BindNamed(sqlQuery, dbMembership)
	if err != nil {
		return database.ProcessSQLErrorf(err, "Failed to bind membership object")
	}

	_, err = db.ExecContext(ctx, query, arg...)
	if err != nil {
		return database.ProcessSQLErrorf(err, "Failed to update membership role")
	}

	membership.Updated = dbMembership.Updated

	return nil
}

// Delete deletes the membership.
func (s *MembershipStore) Delete(ctx context.Context, key types.MembershipKey) error {
	const sqlQuery = `
	DELETE from memberships
	WHERE membership_space_id = $1 AND
	      membership_principal_id = $2`

	db := dbtx.GetAccessor(ctx, s.db)

	if _, err := db.ExecContext(ctx, sqlQuery, key.SpaceID, key.PrincipalID); err != nil {
		return database.ProcessSQLErrorf(err, "delete membership query failed")
	}
	return nil
}

// CountUsers returns a number of users memberships that matches the provided filter.
func (s *MembershipStore) CountUsers(ctx context.Context,
	spaceID int64,
	filter types.MembershipFilter,
) (int64, error) {
	stmt := database.Builder.
		Select("count(*)").
		From("memberships").
		InnerJoin("principals ON membership_principal_id = principal_id").
		Where("membership_space_id = ?", spaceID)

	stmt = prepareMembershipListUsersStmt(stmt, filter)

	sql, args, err := stmt.ToSql()
	if err != nil {
		return 0, fmt.Errorf("failed to convert membership count query to sql: %w", err)
	}

	db := dbtx.GetAccessor(ctx, s.db)

	var count int64
	err = db.QueryRowContext(ctx, sql, args...).Scan(&count)
	if err != nil {
		return 0, database.ProcessSQLErrorf(err, "Failed executing membership count query")
	}

	return count, nil
}

// ListUsers returns a list of memberships for a space or a user.
func (s *MembershipStore) ListUsers(ctx context.Context,
	spaceID int64,
	filter types.MembershipFilter,
) ([]types.MembershipUser, error) {
	const columns = membershipColumns + "," + principalInfoCommonColumns
	stmt := database.Builder.
		Select(columns).
		From("memberships").
		InnerJoin("principals ON membership_principal_id = principal_id").
		Where("membership_space_id = ?", spaceID)

	stmt = prepareMembershipListUsersStmt(stmt, filter)
	stmt = stmt.Limit(database.Limit(filter.Size))
	stmt = stmt.Offset(database.Offset(filter.Page, filter.Size))

	order := filter.Order
	if order == enum.OrderDefault {
		order = enum.OrderAsc
	}

	switch filter.Sort {
	case enum.MembershipSortName:
		stmt = stmt.OrderBy("principal_display_name " + order.String())
	case enum.MembershipSortCreated:
		stmt = stmt.OrderBy("membership_created " + order.String())
	}

	sql, args, err := stmt.ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to convert membership users list query to sql: %w", err)
	}

	dst := make([]*membershipPrincipal, 0)

	db := dbtx.GetAccessor(ctx, s.db)

	if err = db.SelectContext(ctx, &dst, sql, args...); err != nil {
		return nil, database.ProcessSQLErrorf(err, "Failed executing membership users list query")
	}

	result, err := s.mapToMembershipUsers(ctx, dst)
	if err != nil {
		return nil, fmt.Errorf("failed to map memberships users to external type: %w", err)
	}

	return result, nil
}

func prepareMembershipListUsersStmt(
	stmt squirrel.SelectBuilder,
	opts types.MembershipFilter,
) squirrel.SelectBuilder {
	if opts.Query != "" {
		searchTerm := "%%" + strings.ToLower(opts.Query) + "%%"
		stmt = stmt.Where("LOWER(principal_display_name) LIKE ?", searchTerm)
	}

	return stmt
}

// ListSpaces returns a list of spaces in which the provided user is a member.
func (s *MembershipStore) ListSpaces(ctx context.Context,
	userID int64,
) ([]types.MembershipSpace, error) {
	const columns = membershipColumns + "," + spaceColumnsForJoin
	stmt := database.Builder.
		Select(columns).
		From("memberships").
		InnerJoin("spaces ON spaces.space_id = membership_space_id").
		InnerJoin(`paths ON spaces.space_id=paths.path_space_id AND paths.path_is_primary=true`).
		Where("membership_principal_id = ?", userID).
		OrderBy("space_path asc")

	sql, args, err := stmt.ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to convert membership spaces list query to sql: %w", err)
	}

	db := dbtx.GetAccessor(ctx, s.db)

	dst := make([]*membershipSpace, 0)
	if err = db.SelectContext(ctx, &dst, sql, args...); err != nil {
		return nil, database.ProcessSQLErrorf(err, "Failed executing custom list query")
	}

	result, err := s.mapToMembershipSpaces(ctx, dst)
	if err != nil {
		return nil, fmt.Errorf("failed to map memberships spaces to external type: %w", err)
	}

	return result, nil
}

func mapToMembership(m *membership) types.Membership {
	return types.Membership{
		MembershipKey: types.MembershipKey{
			SpaceID:     m.SpaceID,
			PrincipalID: m.PrincipalID,
		},
		CreatedBy: m.CreatedBy,
		Created:   m.Created,
		Updated:   m.Updated,
		Role:      m.Role,
	}
}

func mapToInternalMembership(m *types.Membership) membership {
	return membership{
		SpaceID:     m.SpaceID,
		PrincipalID: m.PrincipalID,
		CreatedBy:   m.CreatedBy,
		Created:     m.Created,
		Updated:     m.Updated,
		Role:        m.Role,
	}
}

func (s *MembershipStore) addPrincipalInfos(ctx context.Context, m *types.Membership) (types.MembershipUser, error) {
	var result types.MembershipUser

	// pull principal infos from cache
	infoMap, err := s.pCache.Map(ctx, []int64{m.CreatedBy, m.PrincipalID})
	if err != nil {
		return result, fmt.Errorf("failed to load membership principal infos: %w", err)
	}

	if user, ok := infoMap[m.PrincipalID]; ok {
		result.Principal = *user
	} else {
		return result, fmt.Errorf("failed to find membership principal info: %w", err)
	}

	if addedBy, ok := infoMap[m.CreatedBy]; ok {
		result.AddedBy = *addedBy
	}

	result.Membership = *m

	return result, nil
}

func (s *MembershipStore) mapToMembershipUsers(ctx context.Context,
	ms []*membershipPrincipal,
) ([]types.MembershipUser, error) {
	// collect all principal IDs
	ids := make([]int64, 0, len(ms))
	for _, m := range ms {
		ids = append(ids, m.membership.CreatedBy)
	}

	// pull principal infos from cache
	infoMap, err := s.pCache.Map(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("failed to load membership principal infos: %w", err)
	}

	// attach the principal infos back to the slice items
	res := make([]types.MembershipUser, len(ms))
	for i, m := range ms {
		res[i].Membership = mapToMembership(&m.membership)
		res[i].Principal = mapToPrincipalInfo(&m.principalInfo)
		if addedBy, ok := infoMap[m.membership.CreatedBy]; ok {
			res[i].AddedBy = *addedBy
		}
	}

	return res, nil
}

func (s *MembershipStore) mapToMembershipSpaces(ctx context.Context,
	ms []*membershipSpace,
) ([]types.MembershipSpace, error) {
	// collect all principal IDs
	ids := make([]int64, 0, len(ms))
	for _, m := range ms {
		ids = append(ids, m.membership.CreatedBy)
	}

	// pull principal infos from cache
	infoMap, err := s.pCache.Map(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("failed to load membership principal infos: %w", err)
	}

	// attach the principal infos back to the slice items
	res := make([]types.MembershipSpace, len(ms))
	for i, m := range ms {
		res[i].Membership = mapToMembership(&m.membership)
		res[i].Space = mapToSpace(&m.space)
		if addedBy, ok := infoMap[m.membership.CreatedBy]; ok {
			res[i].AddedBy = *addedBy
		}
	}

	return res, nil
}