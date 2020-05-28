// Copyright The Linux Foundation and each contributor to CommunityBridge.
// SPDX-License-Identifier: MIT

package main

import (
	"os"

	"github.com/communitybridge/easycla/cla-backend-go/cmd/functional_tests/cla_manager"
	"github.com/communitybridge/easycla/cla-backend-go/cmd/functional_tests/health"
	"github.com/communitybridge/easycla/cla-backend-go/cmd/functional_tests/signatures"

	"github.com/communitybridge/easycla/cla-backend-go/cmd/functional_tests/approval_list"

	"github.com/communitybridge/easycla/cla-backend-go/cmd/functional_tests/test_models"
	log "github.com/communitybridge/easycla/cla-backend-go/logging"
	"github.com/verdverm/frisby"
)

const (
	// API_URL is the development API endpoint
	API_URL = "https://api.dev.lfcla.com" // nolint
	// V2_API_URL is the API endpoint that goes through the API-GW + ACS
	V2_API_URL = "https://api-gw.dev.platform.linuxfoundation.org/cla-service" // nolint
)

func init() {
	frisby.Global.PrintProgressName = true
}

func loadUser(prefix string) test_models.Auth0Config {
	auth0Email := os.Getenv(prefix + "_EMAIL")
	if auth0Email == "" {
		log.Warnf("Unable to run tests - missing %s_EMAIL environment variable", prefix)
		os.Exit(1)
	}
	auth0Username := os.Getenv(prefix + "_USERNAME")
	if auth0Username == "" {
		log.Warnf("Unable to run tests - missing %s_USERNAME environment variable", prefix)
		os.Exit(1)
	}
	auth0Password := os.Getenv(prefix + "_PASSWORD")
	if auth0Password == "" {
		log.Warnf("Unable to run tests - missing %s_PASSWORD environment variable", prefix)
		os.Exit(1)
	}
	auth0ClientID := os.Getenv(prefix + "_CLIENT_ID")
	if auth0ClientID == "" {
		log.Warnf("Unable to run tests - missing %s_CLIENT_ID environment variable", prefix)
		os.Exit(1)
	}

	return test_models.Auth0Config{
		Auth0Email:    auth0Email,
		Auth0UserName: auth0Username,
		Auth0Password: auth0Password,
		Auth0ClientID: auth0ClientID,
	}
}

func main() {
	// Direct V4 API
	apiURL := os.Getenv("API_URL")
	if apiURL == "" {
		apiURL = API_URL
	}
	log.Debugf("API_URL    : %s", apiURL)

	// V2 API URL goes through the API-GW and ACS
	v2APIURL := os.Getenv("V2_API_URL")
	if v2APIURL == "" {
		v2APIURL = V2_API_URL
	}
	log.Debugf("v2_API_URL : %s", v2APIURL)

	auth0User1Config := loadUser("AUTH0_USER1")
	auth0User2Config := loadUser("AUTH0_USER2")
	auth0User3Config := loadUser("AUTH0_USER3")
	auth0User4Config := loadUser("AUTH0_USER4")

	health.NewTestBehaviour(v2APIURL, auth0User1Config).RunAllTests()
	if false {
		cla_manager.NewTestBehaviour(apiURL, auth0User1Config, auth0User2Config).RunAllTests()
		signatures.NewTestBehaviour(apiURL, auth0User1Config, auth0User2Config).RunAllTests()
	}
	approval_list.NewTestBehaviour(v2APIURL, auth0User1Config, auth0User2Config, auth0User3Config, auth0User4Config).RunAllTests()
	frisby.Global.PrintReport()
}
