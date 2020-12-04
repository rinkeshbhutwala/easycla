// Copyright The Linux Foundation and each contributor to CommunityBridge.
// SPDX-License-Identifier: MIT

package cla_groups

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go/aws"

	"golang.org/x/sync/errgroup"

	"github.com/LF-Engineering/lfx-kit/auth"
	v1ClaManager "github.com/communitybridge/easycla/cla-backend-go/cla_manager"
	"github.com/communitybridge/easycla/cla-backend-go/events"
	"github.com/communitybridge/easycla/cla-backend-go/gerrits"
	"github.com/communitybridge/easycla/cla-backend-go/repositories"
	signatureService "github.com/communitybridge/easycla/cla-backend-go/signatures"
	"github.com/communitybridge/easycla/cla-backend-go/v2/metrics"
	organization_service "github.com/communitybridge/easycla/cla-backend-go/v2/organization-service"

	"github.com/communitybridge/easycla/cla-backend-go/utils"

	"github.com/jinzhu/copier"

	v1Models "github.com/communitybridge/easycla/cla-backend-go/gen/models"
	"github.com/communitybridge/easycla/cla-backend-go/gen/restapi/operations/signatures"
	"github.com/communitybridge/easycla/cla-backend-go/gen/v2/models"
	log "github.com/communitybridge/easycla/cla-backend-go/logging"
	v1Project "github.com/communitybridge/easycla/cla-backend-go/project"
	"github.com/communitybridge/easycla/cla-backend-go/projects_cla_groups"
	v1Template "github.com/communitybridge/easycla/cla-backend-go/template"
	v2ProjectService "github.com/communitybridge/easycla/cla-backend-go/v2/project-service"
	"github.com/sirupsen/logrus"
)

type service struct {
	v1ProjectService      v1Project.Service
	v1TemplateService     v1Template.Service
	projectsClaGroupsRepo projects_cla_groups.Repository
	claManagerRequests    v1ClaManager.IService
	signatureService      signatureService.SignatureService
	metricsRepo           metrics.Repository
	gerritService         gerrits.Service
	repositoriesService   repositories.Service
	eventsService         events.Service
}

// Service interface
type Service interface {
	CreateCLAGroup(ctx context.Context, input *models.CreateClaGroupInput, projectManagerLFID string) (*models.ClaGroupSummary, error)
	UpdateCLAGroup(ctx context.Context, claGroupModel *v1Models.ClaGroup, input *models.UpdateClaGroupInput, projectManagerLFID string) (*models.ClaGroupSummary, error)
	ListClaGroupsForFoundationOrProject(ctx context.Context, foundationSFID string) (*models.ClaGroupListSummary, error)
	ListAllFoundationClaGroups(ctx context.Context, foundationID *string) (*models.FoundationMappingList, error)
	DeleteCLAGroup(ctx context.Context, claGroupModel *v1Models.ClaGroup, authUser *auth.User) error
	EnrollProjectsInClaGroup(ctx context.Context, claGroupID string, foundationSFID string, projectSFIDList []string) error
	UnenrollProjectsInClaGroup(ctx context.Context, claGroupID string, foundationSFID string, projectSFIDList []string) error
	AssociateCLAGroupWithProjects(ctx context.Context, claGroupID string, foundationSFID string, projectSFIDList []string) error
	UnassociateCLAGroupWithProjects(ctx context.Context, claGroupID string, foundationSFID string, projectSFIDList []string) error
	EnableCLAService(ctx context.Context, projectSFIDList []string) error
	DisableCLAService(ctx context.Context, projectSFIDList []string) error
	ValidateCLAGroup(ctx context.Context, input *models.ClaGroupValidationRequest) (bool, []string)
}

// NewService returns instance of CLA group service
func NewService(projectService v1Project.Service, templateService v1Template.Service, projectsClaGroupsRepo projects_cla_groups.Repository, claMangerRequests v1ClaManager.IService, signatureService signatureService.SignatureService, metricsRepo metrics.Repository, gerritService gerrits.Service, repositoriesService repositories.Service, eventsService events.Service) Service {
	return &service{
		v1ProjectService:      projectService, // aka cla_group service of v1
		v1TemplateService:     templateService,
		projectsClaGroupsRepo: projectsClaGroupsRepo,
		claManagerRequests:    claMangerRequests,
		signatureService:      signatureService,
		metricsRepo:           metricsRepo,
		gerritService:         gerritService,
		repositoriesService:   repositoriesService,
		eventsService:         eventsService,
	}
}

func (s *service) CreateCLAGroup(ctx context.Context, input *models.CreateClaGroupInput, projectManagerLFID string) (*models.ClaGroupSummary, error) {
	// Validate the input
	log.WithField("input", input).Debugf("validating create cla group input")
	if input.IclaEnabled == nil ||
		input.CclaEnabled == nil ||
		input.CclaRequiresIcla == nil ||
		input.ClaGroupName == nil ||
		input.FoundationSfid == nil {
		return nil, fmt.Errorf("bad request: required parameters are not passed")
	}

	f := logrus.Fields{
		"function":            "CreateCLAGroup",
		utils.XREQUESTID:      ctx.Value(utils.XREQUESTID),
		"ClaGroupName":        aws.StringValue(input.ClaGroupName),
		"ClaGroupDescription": input.ClaGroupDescription,
		"FoundationSfid":      aws.StringValue(input.FoundationSfid),
		"IclaEnabled":         aws.BoolValue(input.IclaEnabled),
		"CclaEnabled":         aws.BoolValue(input.CclaEnabled),
		"CclaRequiresIcla":    aws.BoolValue(input.CclaRequiresIcla),
		"ProjectSfidList":     strings.Join(input.ProjectSfidList, ","),
		"projectManagerLFID":  projectManagerLFID,
	}

	log.WithFields(f).Debug("validating CLA Group input")
	standaloneProject, err := s.validateClaGroupInput(ctx, input)
	if err != nil {
		log.WithFields(f).Warnf("validation of create cla group input failed")
		return nil, err
	}

	if standaloneProject {
		// For standalone projects, root_project_sfid i.e foundation_sfid and project_sfid will be same - make sure it's
		// in our project list as this will be a Foundation Level CLA Group
		if !isFoundationIDInList(*input.FoundationSfid, input.ProjectSfidList) {
			input.ProjectSfidList = append(input.ProjectSfidList, *input.FoundationSfid)
		}
	}

	// Standalone projects are, by definition, Foundation Level CLA Groups
	foundationLevelCLA := standaloneProject
	// If not a standalone, but we have the Foundation ID in our Project list -> Foundation Level CLA Group
	if !standaloneProject && isFoundationIDInList(*input.FoundationSfid, input.ProjectSfidList) {
		foundationLevelCLA = true
	}

	// Create the CLA Group
	log.WithFields(f).WithField("input", input).Debugf("creating cla group")
	claGroup, err := s.v1ProjectService.CreateCLAGroup(ctx, &v1Models.ClaGroup{
		FoundationSFID:          *input.FoundationSfid,
		FoundationLevelCLA:      foundationLevelCLA,
		ProjectDescription:      input.ClaGroupDescription,
		ProjectCCLAEnabled:      *input.CclaEnabled,
		ProjectCCLARequiresICLA: *input.CclaRequiresIcla,
		ProjectExternalID:       *input.FoundationSfid,
		ProjectACL:              []string{projectManagerLFID},
		ProjectICLAEnabled:      *input.IclaEnabled,
		ProjectName:             *input.ClaGroupName,
		Version:                 "v2",
	})
	if err != nil {
		log.WithFields(f).WithError(err).Warnf("cla group create failed")
		return nil, err
	}
	log.WithFields(f).WithField("cla_group", claGroup).Debugf("cla group created")
	f["claGroupID"] = claGroup.ProjectID

	// Attach template with cla group
	var templateFields v1Models.CreateClaGroupTemplate
	err = copier.Copy(&templateFields, &input.TemplateFields)
	if err != nil {
		log.WithFields(f).Error("unable to create v1 create cla group template model", err)
		return nil, err
	}
	log.WithFields(f).Debug("attaching cla_group_template")
	if templateFields.TemplateID == "" {
		log.WithFields(f).Debug("using apache style template as template_id is not passed")
		templateFields.TemplateID = v1Template.ApacheStyleTemplateID
	}
	pdfUrls, err := s.v1TemplateService.CreateCLAGroupTemplate(ctx, claGroup.ProjectID, &templateFields)
	if err != nil {
		log.WithFields(f).Warnf("attaching cla_group_template failed, error: %+v", err)
		log.WithFields(f).Debugf("rolling back creation - deleting previously created CLA Group: %s", *input.ClaGroupName)
		deleteErr := s.v1ProjectService.DeleteCLAGroup(ctx, claGroup.ProjectID)
		if deleteErr != nil {
			log.WithFields(f).WithError(deleteErr).Warnf("deleting previously created CLA Group failed")
		}
		return nil, err
	}
	log.WithFields(f).Debug("cla_group_template attached", pdfUrls)

	// Associate the specified projects with our new CLA Group
	err = s.EnrollProjectsInClaGroup(ctx, claGroup.ProjectID, *input.FoundationSfid, input.ProjectSfidList)
	if err != nil {
		// Oops, roll back logic
		log.WithFields(f).Debug("enroll projects in CLA Group failure - deleting created cla group")
		deleteErr := s.v1ProjectService.DeleteCLAGroup(ctx, claGroup.ProjectID)
		if deleteErr != nil {
			log.WithFields(f).Error("deleting created cla group failed - manual cleanup required.", deleteErr)
		}
		return nil, err
	}

	// Build the response model
	subProjectList, err := s.projectsClaGroupsRepo.GetProjectsIdsForClaGroup(claGroup.ProjectID)
	if err != nil {
		return nil, err
	}
	var foundationName string
	projectList := make([]*models.ClaGroupProject, 0)
	for _, p := range subProjectList {
		foundationName = p.FoundationName
		projectList = append(projectList, &models.ClaGroupProject{
			ProjectName:       p.ProjectName,
			ProjectSfid:       p.ProjectSFID,
			RepositoriesCount: p.RepositoriesCount,
		})
	}
	// Sort the project list based on the project name
	sort.Slice(projectList, func(i, j int) bool {
		return projectList[i].ProjectName < projectList[j].ProjectName
	})

	return &models.ClaGroupSummary{
		FoundationLevelCLA:  isFoundationLevelCLA(*input.FoundationSfid, subProjectList),
		CclaEnabled:         claGroup.ProjectCCLAEnabled,
		CclaPdfURL:          pdfUrls.CorporatePDFURL,
		CclaRequiresIcla:    claGroup.ProjectCCLARequiresICLA,
		ClaGroupDescription: claGroup.ProjectDescription,
		ClaGroupID:          claGroup.ProjectID,
		ClaGroupName:        claGroup.ProjectName,
		FoundationSfid:      claGroup.FoundationSFID,
		FoundationName:      foundationName,
		IclaEnabled:         claGroup.ProjectICLAEnabled,
		IclaPdfURL:          pdfUrls.IndividualPDFURL,
		ProjectList:         projectList,
	}, nil
}

func (s *service) UpdateCLAGroup(ctx context.Context, claGroupModel *v1Models.ClaGroup, input *models.UpdateClaGroupInput, projectManagerLFID string) (*models.ClaGroupSummary, error) {
	// Validate the input
	f := logrus.Fields{
		"function":            "UpdateCLAGroup",
		utils.XREQUESTID:      ctx.Value(utils.XREQUESTID),
		"claGroupID":          claGroupModel.ProjectID,
		"ClaGroupName":        input.ClaGroupName,
		"ClaGroupDescription": input.ClaGroupDescription,
	}

	// If we have an input CLA Group name (not empty) and the name doesn't match the current name...Search all the other
	// CLA Groups and identify any name conflicts.
	if input.ClaGroupName != "" && claGroupModel.ProjectName != input.ClaGroupName {
		existingCLAGroup, groupLookupErr := s.v1ProjectService.GetCLAGroupByName(ctx, input.ClaGroupName)
		if groupLookupErr != nil {
			log.WithFields(f).WithError(groupLookupErr).Warnf("update - error looking up CLA Group by name: %s", input.ClaGroupName)
			return nil, groupLookupErr
		}

		// Expecting no/nil result - if we find an existing CLA Group with the specified input name - this is a name conflict - not allowed
		if existingCLAGroup != nil {
			log.WithFields(f).Warnf("found existing CLA Group with name: %s - unable to update", input.ClaGroupName)
			return nil, &utils.CLAGroupNameConflict{
				CLAGroupID:   claGroupModel.ProjectID,
				CLAGroupName: input.ClaGroupName,
				Err:          nil,
			}
		}
	}

	// Update the CLA Group
	log.WithFields(f).WithField("input", input).Debugf("updating cla group...")
	claGroup, err := s.v1ProjectService.UpdateCLAGroup(ctx, &v1Models.ClaGroup{
		ProjectID:          claGroupModel.ProjectID,
		ProjectName:        input.ClaGroupName,
		ProjectDescription: input.ClaGroupDescription,
		// Copy over the existing values
		ProjectExternalID:            claGroupModel.ProjectExternalID,
		FoundationSFID:               claGroupModel.FoundationSFID,
		FoundationLevelCLA:           claGroupModel.FoundationLevelCLA,
		Gerrits:                      claGroupModel.Gerrits,
		GithubRepositories:           claGroupModel.GithubRepositories,
		ProjectACL:                   claGroupModel.ProjectACL,
		ProjectICLAEnabled:           claGroupModel.ProjectICLAEnabled,
		ProjectCCLAEnabled:           claGroupModel.ProjectCCLAEnabled,
		ProjectCCLARequiresICLA:      claGroupModel.ProjectCCLARequiresICLA,
		ProjectIndividualDocuments:   claGroupModel.ProjectIndividualDocuments,
		ProjectCorporateDocuments:    claGroupModel.ProjectCorporateDocuments,
		ProjectMemberDocuments:       claGroupModel.ProjectMemberDocuments,
		ProjectLive:                  claGroupModel.ProjectLive,
		RootProjectRepositoriesCount: claGroupModel.RootProjectRepositoriesCount,
		Version:                      claGroupModel.Version,
	})
	if err != nil {
		log.WithFields(f).WithError(err).Warnf("cla group update failed")
		return nil, err
	}

	// Load the project IDs for this CLA Group
	subProjectList, err := s.projectsClaGroupsRepo.GetProjectsIdsForClaGroup(claGroupModel.ProjectID)
	if err != nil {
		log.WithFields(f).WithError(err).Warnf("problem getting project IDs for CLA Group")
		return nil, err
	}

	var foundationName string
	projectList := make([]*models.ClaGroupProject, 0)
	for _, p := range subProjectList {
		foundationName = p.FoundationName
		projectList = append(projectList, &models.ClaGroupProject{
			ProjectName:       p.ProjectName,
			ProjectSfid:       p.ProjectSFID,
			RepositoriesCount: p.RepositoriesCount,
		})
	}
	// Sort the project list based on the project name
	sort.Slice(projectList, func(i, j int) bool {
		return projectList[i].ProjectName < projectList[j].ProjectName
	})

	claGroupSummary := &models.ClaGroupSummary{
		FoundationLevelCLA:  isFoundationLevelCLA(claGroupModel.FoundationSFID, subProjectList),
		CclaEnabled:         claGroup.ProjectCCLAEnabled,
		CclaRequiresIcla:    claGroup.ProjectCCLARequiresICLA,
		ClaGroupDescription: claGroup.ProjectDescription,
		ClaGroupID:          claGroup.ProjectID,
		ClaGroupName:        claGroup.ProjectName,
		FoundationSfid:      claGroup.FoundationSFID,
		FoundationName:      foundationName,
		IclaEnabled:         claGroup.ProjectICLAEnabled,
		ProjectList:         projectList,
	}

	// Load and set the ICLA template - if set
	var iclaTemplate string
	if claGroup.ProjectICLAEnabled {
		iclaTemplate, err = s.v1ProjectService.GetCLAGroupCurrentICLATemplateURLByID(ctx, claGroupModel.ProjectID)
		if err != nil {
			log.WithFields(f).WithError(err).Warnf("problem getting project ICLA templates for CLA Group")
			return nil, err
		}
		claGroupSummary.IclaPdfURL = iclaTemplate
	}

	// Load and set the CCLA template - if set
	var cclaTemplate string
	if claGroup.ProjectCCLAEnabled {
		cclaTemplate, err = s.v1ProjectService.GetCLAGroupCurrentCCLATemplateURLByID(ctx, claGroupModel.ProjectID)
		if err != nil {
			log.WithFields(f).WithError(err).Warnf("problem getting project CCLA templates for CLA Group")
			return nil, err
		}
		claGroupSummary.CclaPdfURL = cclaTemplate
	}

	return claGroupSummary, nil
}

// ListClaGroupsForFoundationOrProject returns the CLA Group list for the specified foundation ID
func (s *service) ListClaGroupsForFoundationOrProject(ctx context.Context, projectOrFoundationSFID string) (*models.ClaGroupListSummary, error) {
	f := logrus.Fields{
		"functionName":            "ListClaGroupsForFoundationOrProject",
		utils.XREQUESTID:          ctx.Value(utils.XREQUESTID),
		"projectOrFoundationSFID": projectOrFoundationSFID,
	}

	// Our list of CLA Groups associated with this foundation (could be > 1) or project (only 1)
	var v1ClaGroups = new(v1Models.ClaGroups)
	// Our response model for this function
	responseModel := &models.ClaGroupListSummary{List: make([]*models.ClaGroupSummary, 0)}

	// Lookup this foundation or project in the Platform Project Service/SFDC database
	log.WithFields(f).Debug("looking up foundation/project in platform project service...")
	sfProjectModelDetails, projDetailsErr := v2ProjectService.GetClient().GetProject(projectOrFoundationSFID)
	if projDetailsErr != nil {
		log.WithFields(f).Warnf("unable to lookup CLA Group by foundation or project, error: %+v", projDetailsErr)
		return nil, &utils.SFProjectNotFound{ProjectSFID: projectOrFoundationSFID, Err: projDetailsErr}
	}

	if sfProjectModelDetails == nil {
		log.WithFields(f).Warn("unable to lookup CLA Group by foundation or project - empty result")
		return nil, &utils.SFProjectNotFound{ProjectSFID: projectOrFoundationSFID}
	}

	// Lookup the foundation name - need this if we were a project - need to lookup parent ID/Name
	var foundationID = sfProjectModelDetails.ID
	var foundationName = sfProjectModelDetails.Name

	// If it's a project...
	if sfProjectModelDetails.ProjectType == utils.ProjectTypeProject {
		// Since this is a project and not a foundation, we'll want to set he parent foundation ID and name (which is
		// our parent in this case)
		log.WithFields(f).Debug("found 'project' in platform project service.")
		if sfProjectModelDetails.ProjectOutput.Foundation != nil {
			foundationID = sfProjectModelDetails.ProjectOutput.Foundation.ID
			foundationName = sfProjectModelDetails.ProjectOutput.Foundation.Name
			log.WithFields(f).Debugf("using parent foundation ID: %s and name: %s", foundationID, foundationName)
		} else {
			// Project with no parent - must be a standalone - use our ID and Name as the foundation
			foundationID = sfProjectModelDetails.ID
			foundationName = sfProjectModelDetails.Name
			log.WithFields(f).Debugf("no parent - using project as foundation ID: %s and name: %s", foundationID, foundationName)
		}

		log.WithFields(f).Debug("locating CLA Group mapping...")
		projectCLAGroup, lookupErr := s.projectsClaGroupsRepo.GetClaGroupIDForProject(projectOrFoundationSFID)
		if lookupErr != nil {
			log.WithFields(f).Warnf("problem locating CLA group by project id, error: %+v", lookupErr)
			return nil, &utils.ProjectCLAGroupMappingNotFound{ProjectSFID: projectOrFoundationSFID, Err: lookupErr}
		}

		log.WithFields(f).Debugf("loading CLA Group by ID: %s", projectCLAGroup.ClaGroupID)
		v1ClaGroupsByProject, claGroupLoadErr := s.v1ProjectService.GetCLAGroupByID(ctx, projectCLAGroup.ClaGroupID)
		//v1ClaGroupsByProject, prjerr := s.v1ProjectService.GetClaGroupByProjectSFID(projectOrFoundationSFID, DontLoadDetails)
		if claGroupLoadErr != nil {
			log.WithFields(f).Warnf("problem loading CLA group by id, error: %+v", claGroupLoadErr)
			return nil, &utils.CLAGroupNotFound{CLAGroupID: projectCLAGroup.ClaGroupID, Err: claGroupLoadErr}
		}

		v1ClaGroups.Projects = append(v1ClaGroups.Projects, *v1ClaGroupsByProject)

		v1CLAGroupData, v1ClaGroupErr := s.v1ProjectService.GetClaGroupByProjectSFID(ctx, projectOrFoundationSFID, false)
		if v1ClaGroupErr != nil {
			log.WithFields(f).Warnf("problem locating CLA group by project id, error: %+v", v1ClaGroupErr)
			return nil, &utils.CLAGroupNotFound{CLAGroupID: projectOrFoundationSFID, Err: v1ClaGroupErr}
		}

		_, found := Find(v1ClaGroups.Projects, v1CLAGroupData.ProjectID)
		if !found {
			v1ClaGroups.Projects = append(v1ClaGroups.Projects, *v1CLAGroupData)
		}

	} else if sfProjectModelDetails.ProjectType == utils.ProjectTypeProjectGroup {
		log.WithFields(f).Debug("found 'project group' in platform project service. Locating CLA Groups for foundation...")
		projectCLAGroups, lookupErr := s.projectsClaGroupsRepo.GetProjectsIdsForFoundation(projectOrFoundationSFID)
		if lookupErr != nil {
			log.WithFields(f).Warnf("problem locating CLA group by project id, error: %+v", lookupErr)
			return nil, &utils.ProjectCLAGroupMappingNotFound{ProjectSFID: projectOrFoundationSFID, Err: lookupErr}
		}
		log.WithFields(f).Debugf("discovered %d projects based on foundation SFID...", len(projectCLAGroups))

		claGroupsMap := map[string]bool{}
		// Load these CLA Group records in parallel
		var eg errgroup.Group
		for _, projectCLAGroup := range projectCLAGroups {
			// ensure that following goroutine gets a copy of projectSFID
			projectCLAGroupClaGroupID := projectCLAGroup.ClaGroupID
			// No need to re-process the same CLA group
			if _, ok := claGroupsMap[projectCLAGroupClaGroupID]; ok {
				continue
			}

			// Add entry into our map - so we know not to re-process this CLA Group
			claGroupsMap[projectCLAGroupClaGroupID] = true

			// Invoke the go routine - any errors will be handled below
			eg.Go(func() error {
				log.WithFields(f).Debugf("loading CLA Group by ID: %s", projectCLAGroupClaGroupID)
				claGroupModel, claGroupLookupErr := s.v1ProjectService.GetCLAGroupByID(ctx, projectCLAGroupClaGroupID)
				if claGroupLookupErr != nil {
					log.WithFields(f).Warnf("problem locating project by id: %s, error: %+v", projectCLAGroupClaGroupID, claGroupLookupErr)
					return &utils.SFProjectNotFound{ProjectSFID: projectCLAGroupClaGroupID, Err: claGroupLookupErr}
				}

				v1ClaGroups.Projects = append(v1ClaGroups.Projects, *claGroupModel)
				return nil
			})
		}

		// Wait for the go routines to finish
		log.WithFields(f).Debug("waiting for CLA Groups to load...")
		if loadErr := eg.Wait(); loadErr != nil {
			return nil, loadErr
		}

		v1CLAGroupsData, v1ClaGroupErr := s.v1ProjectService.GetClaGroupsByFoundationSFID(ctx, projectOrFoundationSFID, false)
		if v1ClaGroupErr != nil {
			log.WithFields(f).Warnf("problem locating CLA group by project id, error: %+v", v1ClaGroupErr)
			return nil, &utils.CLAGroupNotFound{CLAGroupID: projectOrFoundationSFID, Err: v1ClaGroupErr}
		}

		v1ClaGroups = v1CLAGroupsData

	} else {
		msg := fmt.Sprintf("unsupported foundation/project SFID type: %s", sfProjectModelDetails.ProjectType)
		log.WithFields(f).Warn(msg)
		return nil, errors.New(msg)
	}

	log.WithFields(f).Debugf("Building response model for %d CLA Groups", len(v1ClaGroups.Projects))

	claGroupIDList := utils.NewStringSet()

	// Build the response model for each CLA Group...
	for _, v1ClaGroup := range v1ClaGroups.Projects {

		// Keep a list of the CLA Group IDs - we'll use it later to do a batch look in the metrics
		claGroupIDList.Add(v1ClaGroup.ProjectID)

		currentICLADoc, docErr := v1Project.GetCurrentDocument(context.Background(), v1ClaGroup.ProjectIndividualDocuments)
		if docErr != nil {
			log.WithFields(f).WithError(docErr).Warn("problem determining current ICLA for this CLA Group")
		}
		currentCCLADoc, docErr := v1Project.GetCurrentDocument(context.Background(), v1ClaGroup.ProjectCorporateDocuments)
		if docErr != nil {
			log.WithFields(f).WithError(docErr).Warn("problem determining current CCLA for this CLA Group")
		}

		cg := &models.ClaGroupSummary{
			CclaEnabled:         v1ClaGroup.ProjectCCLAEnabled,
			CclaRequiresIcla:    v1ClaGroup.ProjectCCLARequiresICLA,
			ClaGroupDescription: v1ClaGroup.ProjectDescription,
			ClaGroupID:          v1ClaGroup.ProjectID,
			ClaGroupName:        v1ClaGroup.ProjectName,
			FoundationSfid:      v1ClaGroup.FoundationSFID,
			FoundationName:      foundationName,
			IclaEnabled:         v1ClaGroup.ProjectICLAEnabled,
			IclaPdfURL:          currentICLADoc.DocumentS3URL,
			CclaPdfURL:          currentCCLADoc.DocumentS3URL,
			// Add root_project_repositories_count to repositories_count initially
			RepositoriesCount:            v1ClaGroup.RootProjectRepositoriesCount,
			RootProjectRepositoriesCount: v1ClaGroup.RootProjectRepositoriesCount,
		}

		// How many SF projects are associated with this CLA Group?
		cgprojects, err := s.projectsClaGroupsRepo.GetProjectsIdsForClaGroup(v1ClaGroup.ProjectID)
		if err != nil {
			return nil, &utils.ProjectCLAGroupMappingNotFound{CLAGroupID: v1ClaGroup.ProjectID, Err: err}
		}

		// For each SF project under this CLA Group...
		projectList := make([]*models.ClaGroupProject, 0)
		var foundationLevelCLA = false
		for _, cgproject := range cgprojects {
			projectList = append(projectList, &models.ClaGroupProject{
				ProjectSfid:       cgproject.ProjectSFID,
				ProjectName:       cgproject.ProjectName,
				RepositoriesCount: cgproject.RepositoriesCount,
			})

			if cgproject.ProjectSFID == foundationID {
				foundationLevelCLA = true
			}
		}

		// Update the response model
		cg.FoundationLevelCLA = foundationLevelCLA
		// Sort the project list based on the project name
		sort.Slice(projectList, func(i, j int) bool {
			return projectList[i].ProjectName < projectList[j].ProjectName
		})
		cg.ProjectList = projectList

		// Add this CLA Group to our response model
		responseModel.List = append(responseModel.List, cg)
	}

	// One more pass to update the metrics - bulk lookup the metrics and update the response model
	log.WithFields(f).Debugf("Loading metrics for %d CLA Groups - updating response", len(claGroupIDList.List()))
	var iclaSignatureCount, cclaSignatureCount int64
	for _, responseEntry := range responseModel.List {
		log.Debugf("cla group entry logs %s", responseEntry.ClaGroupID)
		iclaSignatureDetails, err := s.signatureService.GetProjectSignatures(ctx, signatures.GetProjectSignaturesParams{ProjectID: responseEntry.ClaGroupID, ClaType: aws.String(utils.ClaTypeICLA), SignatureType: aws.String(utils.SignatureTypeCLA)})
		if err != nil {
			log.Warnf("error while getting ICLA Signature using clagroupID %s Error: %v", responseEntry.ClaGroupID, err)
		}
		iclaSignatureCount = iclaSignatureDetails.ResultCount

		cclaSignatureDetails, err := s.signatureService.GetProjectSignatures(ctx, signatures.GetProjectSignaturesParams{ProjectID: responseEntry.ClaGroupID, ClaType: aws.String(utils.ClaTypeCCLA), SignatureType: aws.String(utils.SignatureTypeCCLA)})
		if err != nil {
			log.Warnf("error while getting ICLA Signature using clagroupID %s Error: %v", responseEntry.ClaGroupID, err)
		}
		cclaSignatureCount = cclaSignatureDetails.ResultCount

		responseEntry.TotalSignatures = cclaSignatureCount + iclaSignatureCount
	}

	// Sort the response based on the Foundation and CLA group name
	sort.Slice(responseModel.List, func(i, j int) bool {
		switch strings.Compare(responseModel.List[i].FoundationName, responseModel.List[j].FoundationName) {
		case -1:
			return true
		case 1:
			return false
		}
		return responseModel.List[i].ClaGroupName < responseModel.List[j].ClaGroupName
	})

	return responseModel, nil
}

func (s *service) ListAllFoundationClaGroups(ctx context.Context, foundationID *string) (*models.FoundationMappingList, error) {
	f := logrus.Fields{
		"functionName":   "ListAllFoundationClaGroups",
		utils.XREQUESTID: ctx.Value(utils.XREQUESTID),
		"foundationID":   foundationID,
	}
	log.WithFields(f).Debug("listing all foundation CLA groups...")
	var out []*projects_cla_groups.ProjectClaGroup
	var err error
	if foundationID != nil {
		out, err = s.projectsClaGroupsRepo.GetProjectsIdsForFoundation(*foundationID)
	} else {
		out, err = s.projectsClaGroupsRepo.GetProjectsIdsForAllFoundation()
	}
	if err != nil {
		return nil, err
	}
	return toFoundationMapping(out), nil
}

// DeleteCLAGroup handles deleting and invalidating the CLA group, removing permissions, cleaning up pending requests, etc.
func (s *service) DeleteCLAGroup(ctx context.Context, claGroupModel *v1Models.ClaGroup, authUser *auth.User) error {
	f := logrus.Fields{
		"functionName":             "DeleteCLAGroup",
		utils.XREQUESTID:           ctx.Value(utils.XREQUESTID),
		"claGroupID":               claGroupModel.ProjectID,
		"claGroupExternalID":       claGroupModel.ProjectExternalID,
		"claGroupName":             claGroupModel.ProjectName,
		"claGroupFoundationSFID":   claGroupModel.FoundationSFID,
		"claGroupVersion":          claGroupModel.Version,
		"claGroupICLAEnabled":      claGroupModel.ProjectICLAEnabled,
		"claGroupCCLAEnabled":      claGroupModel.ProjectCCLAEnabled,
		"claGroupCCLARequiresICLA": claGroupModel.ProjectCCLARequiresICLA,
	}
	log.WithFields(f).Debug("deleting CLA Group...")

	oscClient := organization_service.GetClient()

	// Get a list of project CLA Group entries - need to know which SF Projects we're dealing with...
	projectCLAGroupEntries, projErr := s.projectsClaGroupsRepo.GetProjectsIdsForClaGroup(claGroupModel.ProjectID)
	if projErr != nil {
		log.WithFields(f).Warnf("unable to fetch project IDs for CLA Group, error: %+v", projErr)
		return projErr
	}
	log.WithFields(f).Debugf("loading %d Project CLA Group entries", len(projectCLAGroupEntries))

	// Grab the foundation SFID - each project should have the same foundation
	var foundationSFID = ""
	if len(projectCLAGroupEntries) > 0 {
		foundationSFID = projectCLAGroupEntries[0].FoundationSFID
	}

	projectIDList := utils.NewStringSet()
	for _, projectCLAGroupEntry := range projectCLAGroupEntries {
		// Add the project ID to our list - we'll remove the entry in the Project CLA Group in a bit...
		projectIDList.Add(projectCLAGroupEntry.ProjectSFID)
	}

	// Note: most of these delete/cleanup calls are done in a go routine
	// Error channel to send back the results
	errChan := make(chan error)
	var goRoutineCount = 0

	// Locate all the signed/approved corporate CLA signature records - need all the Organization IDs so we can
	// remove CLA Manager/CLA Manager Designee/CLA Signatory Permissions
	log.WithFields(f).Debug("locating signed corporate signatures...")
	signatureCompanyIDModels, companyIDErr := s.signatureService.GetCompanyIDsWithSignedCorporateSignatures(ctx, claGroupModel.ProjectID)
	if companyIDErr != nil {
		log.WithFields(f).Warnf("unable to fetch list of company IDs, error: %+v", companyIDErr)
		return companyIDErr
	}
	log.WithFields(f).Debugf("discovered %d corporate signatures to investigate", len(signatureCompanyIDModels))

	go func(claGroup *v1Models.ClaGroup, authUser *auth.User) {
		// Delete gerrit repositories
		log.WithFields(f).Debug("deleting CLA Group gerrits...")
		numDeleted, err := s.gerritService.DeleteClaGroupGerrits(claGroup.ProjectID)
		if err != nil {
			log.WithFields(f).Warn(err)
			errChan <- err
			return
		}

		if numDeleted > 0 {
			log.WithFields(f).Debugf("deleted %d gerrit repositories", numDeleted)
			// Log gerrit event
			s.eventsService.LogEvent(&events.LogEventArgs{
				EventType:     events.GerritRepositoryDeleted,
				ClaGroupModel: claGroup,
				LfUsername:    authUser.UserName,
				EventData: &events.GerritProjectDeletedEventData{
					DeletedCount: numDeleted,
				},
			})
		} else {
			log.WithFields(f).Debug("no gerrit repositories found to delete")
		}

		// No errors - nice...return nil
		errChan <- nil
	}(claGroupModel, authUser)
	goRoutineCount++

	go func(claGroup *v1Models.ClaGroup, authUser *auth.User) {
		// Delete github repositories
		log.WithFields(f).Debug("deleting CLA Group GitHub repositories...")
		numDeleted, delGHReposErr := s.repositoriesService.DisableRepositoriesByProjectID(ctx, claGroup.ProjectID)
		if delGHReposErr != nil {
			log.WithFields(f).Warn(delGHReposErr)
			errChan <- delGHReposErr
			return
		}
		if numDeleted > 0 {
			log.WithFields(f).Debugf("deleted %d github repositories", numDeleted)
			// Log github delete event
			s.eventsService.LogEvent(&events.LogEventArgs{
				EventType:     events.RepositoryDisabled,
				ClaGroupModel: claGroup,
				LfUsername:    authUser.UserName,
				EventData: &events.GithubProjectDeletedEventData{
					DeletedCount: numDeleted,
				},
			})
		} else {
			log.WithFields(f).Debug("no github repositories found to delete")
		}

		// No errors - nice...return nil
		errChan <- nil
	}(claGroupModel, authUser)
	goRoutineCount++

	go func(claGroup *v1Models.ClaGroup, authUser *auth.User) {
		// Delete github repositories
		log.WithFields(f).Debug("deleting CLA Group GitHub orgs...")
		numDeleted, delGHReposErr := s.repositoriesService.DeleteGithubOrgByRepositoryDetails(ctx, claGroup.ProjectID)
		if delGHReposErr != nil {
			log.WithFields(f).Warn(delGHReposErr)
			errChan <- delGHReposErr
			return
		}
		if numDeleted > 0 {
			log.WithFields(f).Debugf("deleted %d github repositories", numDeleted)
			// Log github delete event
			s.eventsService.LogEvent(&events.LogEventArgs{
				EventType:     events.GithubOrganizationDeleted,
				ClaGroupModel: claGroup,
				LfUsername:    authUser.UserName,
				EventData: &events.GithubProjectDeletedEventData{
					DeletedCount: numDeleted,
				},
			})
		} else {
			log.WithFields(f).Debug("no github orgs found to delete")
		}

		// No errors - nice...return nil
		errChan <- nil
	}(claGroupModel, authUser)
	goRoutineCount++

	// Invalidate project signatures
	go func(claGroup *v1Models.ClaGroup, authUser *auth.User) {
		log.WithFields(f).Debug("invalidating all signatures for CLA Group...")
		numInvalidated, invalidateErr := s.signatureService.InvalidateProjectRecords(ctx, claGroup.ProjectID, claGroup.ProjectName)
		if invalidateErr != nil {
			log.WithFields(f).Warn(invalidateErr)
			errChan <- invalidateErr
			return
		}

		if numInvalidated > 0 {
			log.WithFields(f).Debugf("invalidated %d signatures", numInvalidated)
			// Log invalidate signatures
			s.eventsService.LogEvent(&events.LogEventArgs{
				EventType:     events.InvalidatedSignature,
				ClaGroupModel: claGroup,
				LfUsername:    authUser.UserName,
				EventData: &events.SignatureProjectInvalidatedEventData{
					InvalidatedCount: numInvalidated,
				},
			})
		} else {
			log.WithFields(f).Debug("no signatures found to invalidate")
		}

		// No errors - nice...return nil
		errChan <- nil
	}(claGroupModel, authUser)
	goRoutineCount++

	// Basically, we want to clean up all who have: Project|Organization scope (corporate console stuff)
	// For each organization/company...
	log.WithFields(f).Debug("locating users with cla-manager, cla-signatory, and cla-manager-designee for ProjectSFID|CompanySFID scope - need to remove the roles from the users...")
	for _, signatureCompanyIDModel := range signatureCompanyIDModels {

		// Delete any CLA Manager requests
		go func(companyID, projectID string) {
			log.WithFields(f).Debugf("locating CLA Manager requests for company: %s", signatureCompanyIDModel.CompanyName)
			// Fetch any pending CLA manager requests for this company/project
			requestList, requestErr := s.claManagerRequests.GetRequests(companyID, projectID)
			if requestErr != nil {
				log.WithFields(f).Warn(requestErr)
				errChan <- requestErr
				return
			}

			// If we have any CLA manager requests - delete them
			if requestList != nil && len(requestList.Requests) > 0 {
				log.WithFields(f).Debugf("removing %d CLA Manager Requests found for company and project", len(requestList.Requests))
				for _, request := range requestList.Requests {
					reqDelErr := s.claManagerRequests.DeleteRequest(request.RequestID)
					log.WithFields(f).Warn(reqDelErr)
					errChan <- reqDelErr
					return
				}
			} else {
				log.WithFields(f).Debug("no CLA Manager Requests found for company and project")
			}

			// No errors - nice...return nil
			errChan <- nil
		}(signatureCompanyIDModel.CompanyID, claGroupModel.ProjectID)
		goRoutineCount++

		// For each project associated with the CLA Group...
		for _, pID := range projectIDList.List() {

			// Remove CLA Manager role
			go func(companySFID, projectSFID string, authUser *auth.User) {
				log.WithFields(f).Debugf("removing role permissions for %s...", utils.CLAManagerRole)
				claMgrErr := oscClient.DeleteRolePermissions(companySFID, projectSFID, utils.CLAManagerRole, authUser)
				if claMgrErr != nil {
					log.WithFields(f).Warn(claMgrErr)
					errChan <- claMgrErr
					return
				}

				// No errors - nice...return nil
				errChan <- nil
			}(signatureCompanyIDModel.CompanySFID, pID, authUser)
			goRoutineCount++

			// Remove CLA Manager Designee
			go func(companySFID, projectSFID string, authUser *auth.User) {
				log.WithFields(f).Debugf("removing role permissions for %s...", utils.CLADesigneeRole)
				claMgrDesigneeErr := oscClient.DeleteRolePermissions(companySFID, projectSFID, utils.CLADesigneeRole, authUser)
				if claMgrDesigneeErr != nil {
					log.WithFields(f).Warn(claMgrDesigneeErr)
					errChan <- claMgrDesigneeErr
					return
				}
				// No errors - nice...return nil
				errChan <- nil
			}(signatureCompanyIDModel.CompanySFID, pID, authUser)
			goRoutineCount++

			// Remove CLA signatories role
			go func(companySFID, projectSFID string, authUser *auth.User) {
				log.WithFields(f).Debugf("removing role permissions for %s...", utils.CLASignatoryRole)
				claSignatoryErr := oscClient.DeleteRolePermissions(companySFID, projectSFID, utils.CLASignatoryRole, authUser)
				if claSignatoryErr != nil {
					log.WithFields(f).Warn(claSignatoryErr)
					errChan <- claSignatoryErr
					return
				}

				// No errors - nice...return nil
				errChan <- nil
			}(signatureCompanyIDModel.CompanySFID, pID, authUser)
			goRoutineCount++
		}
	}

	// Process the results
	log.WithFields(f).Debugf("waiting for %d go routines to complete...", goRoutineCount)
	for i := 0; i < goRoutineCount; i++ {
		errFromFunc := <-errChan
		if errFromFunc != nil {
			log.WithFields(f).Warnf("problem removing removing requests or removing permissions, error: %+v - continuing with CLA Group delete", errFromFunc)
			return errFromFunc
		}
	}

	// Associate the specified projects with our new CLA Group
	err := s.UnenrollProjectsInClaGroup(ctx, claGroupModel.ProjectID, foundationSFID, projectIDList.List())
	if err != nil {
		log.WithFields(f).WithError(err).Warn("unenrolling projects in CLA Group failed - manual cleanup required.")
	}

	// Finally, delete the CLA Group last...
	log.WithFields(f).Debug("finally, deleting cla_group from dynamodb")
	err = s.v1ProjectService.DeleteCLAGroup(ctx, claGroupModel.ProjectID)
	if err != nil {
		log.WithFields(f).Warnf("problem deleting cla_group, error: %+v", err)
		return err
	}

	return nil
}

// EnrollProjectsInClaGroup enrolls the specified project list in the CLA Group
func (s *service) EnrollProjectsInClaGroup(ctx context.Context, claGroupID string, foundationSFID string, projectSFIDList []string) error {
	f := logrus.Fields{
		"functionName":    "EnrollProjectsInClaGroup",
		utils.XREQUESTID:  ctx.Value(utils.XREQUESTID),
		"claGroupID":      claGroupID,
		"foundationSFID":  foundationSFID,
		"projectSFIDList": strings.Join(projectSFIDList, ","),
	}

	log.WithFields(f).Debug("validating enroll project input")
	err := s.validateEnrollProjectsInput(ctx, foundationSFID, projectSFIDList)
	if err != nil {
		log.WithFields(f).Warnf("validating enroll project input failed. error = %s", err)
		return err
	}

	// Setup a wait group to enroll and enable CLA service - we'll want to work quickly here
	var errorList []error
	var wg sync.WaitGroup
	wg.Add(2)

	// Separate go routine for enrolling projects
	go func(c context.Context, claGrID string, fSFID string, projSFIDList []string) {
		defer wg.Done()
		log.WithFields(f).Debug("enrolling projects in CLA Group")
		enrollErr := s.AssociateCLAGroupWithProjects(c, claGrID, fSFID, projSFIDList)
		if enrollErr != nil {
			log.WithFields(f).WithError(enrollErr).Warn("enrolling projects in CLA Group failed")
			errorList = append(errorList, enrollErr)
		}

	}(ctx, claGroupID, foundationSFID, projectSFIDList)

	// Separate go routine for enabling the CLA Service in the project service
	go func(c context.Context, projSFIDList []string) {
		defer wg.Done()
		log.WithFields(f).Debug("enabling CLA service in platform project service")
		errEnableCLA := s.EnableCLAService(c, projSFIDList)
		if errEnableCLA != nil {
			log.WithFields(f).WithError(errEnableCLA).Warn("enabling CLA service in platform project service failed")
			errorList = append(errorList, errEnableCLA)
		}
	}(ctx, projectSFIDList)

	// Wait until all go routines are done
	wg.Wait()
	if len(errorList) > 0 {
		log.WithFields(f).WithError(errorList[0]).Warnf("encountered %d errors when enrolling and enabling CLA service for %d projects", len(errorList), len(projectSFIDList))
		return errorList[0]
	}

	return nil
}

func (s *service) UnenrollProjectsInClaGroup(ctx context.Context, claGroupID string, foundationSFID string, projectSFIDList []string) error {
	f := logrus.Fields{
		"functionName":    "UnenrollProjectsInClaGroup",
		utils.XREQUESTID:  ctx.Value(utils.XREQUESTID),
		"claGroupID":      claGroupID,
		"foundationSFID":  foundationSFID,
		"projectSFIDList": strings.Join(projectSFIDList, ","),
	}

	log.WithFields(f).Debug("validating unenroll project input")
	err := s.validateUnenrollProjectsInput(ctx, foundationSFID, projectSFIDList)
	if err != nil {
		log.WithFields(f).Warnf("validating unenroll project input failed. error = %s", err)
		return err
	}

	// Setup a wait group to enroll and enable CLA service - we'll want to work quickly here
	var errorList []error
	var wg sync.WaitGroup
	wg.Add(2)

	// Separate go routine for unenrolling projects
	go func(c context.Context, claGrID string, fSFID string, projSFIDList []string) {
		defer wg.Done()
		log.WithFields(f).Debug("unenrolling projects in CLA Group")
		unenrollErr := s.UnassociateCLAGroupWithProjects(c, claGrID, fSFID, projSFIDList)
		if unenrollErr != nil {
			log.WithFields(f).WithError(unenrollErr).Warn("unenrolling projects in CLA Group failed")
			errorList = append(errorList, unenrollErr)
		}

	}(ctx, claGroupID, foundationSFID, projectSFIDList)

	// Separate go routine for disabling the CLA Service in the project service
	go func(c context.Context, projSFIDList []string) {
		defer wg.Done()
		log.WithFields(f).Debug("disabling CLA service in platform project service")
		errDisableCLA := s.DisableCLAService(c, projSFIDList)
		if errDisableCLA != nil {
			log.WithFields(f).WithError(errDisableCLA).Warn("disabling CLA service in platform project service failed")
			errorList = append(errorList, errDisableCLA)
		}
	}(ctx, projectSFIDList)

	// Wait until all go routines are done
	wg.Wait()
	if len(errorList) > 0 {
		log.WithFields(f).WithError(errorList[0]).Warnf("encountered %d errors when unenrolling and disabling CLA service for %d projects", len(errorList), len(projectSFIDList))
		return errorList[0]
	}

	return nil
}

// ValidateCLAGroup is the service handler for validating a CLA Group
func (s *service) ValidateCLAGroup(ctx context.Context, input *models.ClaGroupValidationRequest) (bool, []string) {

	var valid = true
	var validationErrors []string

	// All parameters are optional - caller can specify which fields they want to validate based on what they provide
	// in the request payload.  If the value is there, we will attempt to validate it.  Note: some validation
	// happens at the Swagger API specification level (and rejected) before our API handler will be invoked.

	// Note: CLA Group Name Min/Max Character Length validated via Swagger Spec restrictions
	if input.ClaGroupName != nil {
		claGroupModel, err := s.v1ProjectService.GetCLAGroupByName(ctx, *input.ClaGroupName)
		if err != nil {
			valid = false
			validationErrors = append(validationErrors, fmt.Sprintf("unable to query project service - error: %+v", err))
		}
		if claGroupModel != nil {
			valid = false
			validationErrors = append(validationErrors, fmt.Sprintf("CLA Group with name %s already exist", *input.ClaGroupName))
		}
	}

	// Note: CLA Group Description Min/Max Character Length validated via Swagger Spec restrictions

	// Optional - we can expand this API logic to validate other fields if needed.

	return valid, validationErrors
}

// Find . . .
func Find(slice []v1Models.ClaGroup, val string) (int, bool) {
	for i, item := range slice {
		if item.ProjectID == val {
			return i, true
		}
	}
	return -1, false
}
