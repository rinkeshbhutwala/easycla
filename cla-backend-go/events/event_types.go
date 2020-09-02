// Copyright The Linux Foundation and each contributor to CommunityBridge.
// SPDX-License-Identifier: MIT

package events

// event types
const (

	// CreateTemplate event type
	CreateTemplate = "Create Template"

	// AddGithubOrgToWL event type
	AddGithubOrgToWL = "Add GH Org To WL"
	// DeleteGithubOrgFromWL event type
	DeleteGithubOrgFromWL = "Delete GH Org From WL"

	// CreateCCLAApprovalListRequest event type
	CreateCCLAApprovalListRequest = "Create CCLA Approval List Request"
	// DeleteCCLAApprovalListRequest event type
	DeleteCCLAApprovalListRequest = "Delete CCLA Approval List Request"

	// AddUserToCompanyACL event type
	// DeleteUserFromCompanyACL event type
	//DeleteUserFromCompanyACL = "Delete User From Company ACL"

	// DeletePendingInvite event type
	AddGithubOrganization    = "Add Github Organization"
	DeleteGithubOrganization = "Delete Github Organization"
)

// events
// naming convention : <resource>.<action>
const (
	CLATemplateCreated = "cla_template.created"
	UserCreated        = "user.created"
	UserUpdated        = "user.updated"
	UserDeleted        = "user.deleted"

	RepositoryAdded    = "repository.added"
	RepositoryDisabled = "repository.disabled"

	GerritRepositoryAdded   = "gerrit_repository.added"
	GerritRepositoryDeleted = "gerrit_repository.deleted"

	GithubOrganizationAdded   = "github_organization.added"
	GithubOrganizationDeleted = "github_organization.deleted"
	GithubOrganizationUpdated = "github_organization.updated"

	CompanyACLUserAdded       = "company_acl.user_added"
	CompanyACLRequestAdded    = "company_acl.request_added"
	CompanyACLRequestApproved = "company_acl.request_approved"
	CompanyACLRequestDenied   = "company_acl.request_denied"

	CCLAApprovalListRequestCreated  = "ccla_approval_list_request.created"
	CCLAApprovalListRequestApproved = "ccla_approval_list_request.approved"
	CCLAApprovalListRequestRejected = "ccla_approval_list_request.rejected"

	ApprovalListGithubOrganizationAdded   = "approval_list.github_organization_added"
	ApprovalListGithubOrganizationDeleted = "approval_list.github_organization_deleted"

	ClaManagerAccessRequestCreated  = "cla_manager.access_request_created"
	ClaManagerAccessRequestApproved = "cla_manager.access_request_approved"
	ClaManagerAccessRequestDenied   = "cla_manager.access_request_denied"
	ClaManagerAccessRequestDeleted  = "cla_manager.access_request_deleted"

	ClaApprovalListUpdated = "cla_manager.approval_list_updated"

	ClaManagerCreated     = "cla_manager.added"
	ClaManagerDeleted     = "cla_manager.deleted"
	ClaManagerRoleCreated = "cla_manager.added"
	ClaManagerRoleDeleted = "cla_manager.deleted"

	CLAGroupCreated = "cla_group.created"
	CLAGroupUpdated = "cla_group.updated"
	CLAGroupDeleted = "cla_group.deleted"

	InvalidatedSignature = "signature.invalidated"

	ContributorNotifyCompanyAdminType = "contributor.notify_company_admin"
	ContributorNotifyCLADesigneeType  = "contributor.notify_cla_designee"
	ContributorAssignCLADesigneeType  = "contributor.assign_designee"
	ConvertUserToContactType          = "lfx_user.convert_to_contact"
	AssignUserRoleScopeType           = "lfx_org_service.assign_user_role_scope"
)
