package openshift

import (
	"bytes"
	"errors"
	"io/ioutil"
	"log"
	"net/http"
	"strings"

	"fmt"

	"crypto/tls"
	"github.com/Jeffail/gabs"
	"github.com/SchweizerischeBundesbahnen/ssp-backend/server/common"
	"github.com/gin-gonic/gin"
	"gopkg.in/gomail.v2"
	"os"
)

func (p Plugin) newProjectHandler(c *gin.Context) {
	username := common.GetUserName(c)

	var data common.NewProjectCommand
	if c.BindJSON(&data) == nil {
		if err := validateNewProject(data.Project, data.Billing, false); err != nil {
			c.JSON(http.StatusBadRequest, common.ApiResponse{Message: err.Error()})
			return
		}

		if err := p.createNewProject(data.ClusterId, data.Project, username, data.Billing, data.MegaId, false); err != nil {
			c.JSON(http.StatusBadRequest, common.ApiResponse{Message: err.Error()})
		} else {
			err := sendNewProjectMail(data.ClusterId, data.Project, username, data.MegaId)
			if err != nil {
				log.Printf("Can't send e-mail about new project (%v) on cluster %v.", err, data.ClusterId)
			}

			c.JSON(http.StatusOK, common.ApiResponse{
				Message: fmt.Sprintf("Das Projekt %v wurde erstellt auf Cluster %v", data.Project, data.ClusterId),
			})
		}
	} else {
		c.JSON(http.StatusBadRequest, common.ApiResponse{Message: wrongAPIUsageError})
	}
}

func (p Plugin) newTestProjectHandler(c *gin.Context) {
	username := common.GetUserName(c)

	var data common.NewTestProjectCommand
	if c.BindJSON(&data) == nil {
		// Special values for a test project
		billing := "keine-verrechnung"
		data.Project = username + "-" + data.Project

		if err := validateNewProject(data.Project, billing, true); err != nil {
			c.JSON(http.StatusBadRequest, common.ApiResponse{Message: err.Error()})
			return
		}

		if err := p.createNewProject(data.ClusterId, data.Project, username, billing, "", true); err != nil {
			c.JSON(http.StatusBadRequest, common.ApiResponse{Message: err.Error()})
		} else {
			c.JSON(http.StatusOK, common.ApiResponse{
				Message: fmt.Sprintf("Das Test-Projekt %v wurde erstellt auf Cluster %v", data.Project, data.ClusterId),
			})
		}
	} else {
		c.JSON(http.StatusBadRequest, common.ApiResponse{Message: wrongAPIUsageError})
	}
}

func (p Plugin) getProjectsHandler(c *gin.Context) {
	username := common.GetUserName(c)
	params := c.Request.URL.Query()
	clusterId := params.Get("clusterid")
	if clusterId == "" {
		c.JSON(http.StatusBadRequest, common.ApiResponse{Message: wrongAPIUsageError})
		return
	}
	log.Printf("%v has queried all his projects in clusterid: %v", username, clusterId)
	projects, err := p.getUserProjects(clusterId, username)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.ApiResponse{Message: err.Error()})
	} else {
		c.JSON(http.StatusOK, projects)
	}
}

func (p Plugin) getUserProjects(clusterid, username string) ([]string, error) {
	// TODO: only return projects, where the user has access
	resp, err := p.getOseHTTPClient("GET", clusterid, "oapi/v1/projects", nil)
	if err != nil {
		return []string{}, err
	}

	defer resp.Body.Close()

	json, err := gabs.ParseJSONBuffer(resp.Body)
	if err != nil {
		log.Println("error decoding json:", err, resp.StatusCode)
		return []string{}, errors.New(genericAPIError)
	}
	projects, err := json.Search("items").Children()
	if err != nil {
		log.Println("error getting projects: ", err)
		return []string{}, errors.New(genericAPIError)
	}
	var projectNames []string
	for _, project := range projects {
		projectNames = append(projectNames, project.Path("metadata.name").Data().(string))
	}
	return projectNames, nil
}

func (p Plugin) getProjectAdminsHandler(c *gin.Context) {
	username := common.GetUserName(c)

	params := c.Request.URL.Query()
	clusterId := params.Get("clusterid")
	project := params.Get("project")

	if clusterId == "" || project == "" {
		c.JSON(http.StatusBadRequest, common.ApiResponse{Message: wrongAPIUsageError})
		return
	}

	log.Printf("%v has queried all the admins of project %v on cluster %v", username, project, clusterId)

	if admins, _, err := p.getProjectAdminsAndOperators(clusterId, project); err != nil {
		c.JSON(http.StatusBadRequest, common.ApiResponse{Message: err.Error()})
	} else {
		c.JSON(http.StatusOK, common.AdminList{
			Admins: admins,
		})
	}
}

func (p Plugin) getProjectInformationHandler(c *gin.Context) {
	username := common.GetUserName(c)

	params := c.Request.URL.Query()
	clusterId := params.Get("clusterid")
	project := params.Get("project")

	if err := p.validateAdminAccess(clusterId, username, project); err != nil {
		c.JSON(http.StatusBadRequest, common.ApiResponse{Message: err.Error()})
		return
	}

	pi, err := p.getProjectInformation(clusterId, project)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.ApiResponse{Message: err.Error()})
	}

	c.JSON(http.StatusOK, pi)
}

func (p Plugin) updateProjectInformationHandler(c *gin.Context) {
	username := common.GetUserName(c)

	var data common.UpdateProjectInformationCommand
	if c.BindJSON(&data) == nil {
		if err := p.validateProjectInformation(data, username); err != nil {
			c.JSON(http.StatusBadRequest, common.ApiResponse{Message: err.Error()})
			return
		}

		if err := p.createOrUpdateMetadata(data.ClusterId, data.Project, data.Billing, data.MegaID, username, false); err != nil {
			c.JSON(http.StatusBadRequest, common.ApiResponse{Message: err.Error()})
		} else {
			c.JSON(http.StatusOK, common.ApiResponse{
				Message: fmt.Sprintf("The details for project %v on cluster %v has been saved", data.Project, data.ClusterId),
			})
		}
	} else {
		c.JSON(http.StatusBadRequest, common.ApiResponse{Message: wrongAPIUsageError})
	}
}

func validateNewProject(project string, billing string, testProject bool) error {
	if len(project) == 0 {
		return errors.New("Project name has to be provided")
	}

	if !testProject && len(billing) == 0 {
		return errors.New("Accounting number must be provided")
	}

	return nil
}

func (p Plugin) validateAdminAccess(clusterId, username, project string) error {
	if clusterId == "" {
		return errors.New("Cluster must be provided")
	}

	if project == "" {
		return errors.New("Project name must be provided")
	}

	// Validate permissions
	if err := p.checkAdminPermissions(clusterId, username, project); err != nil {
		return err
	}

	return nil
}

func (p Plugin) validateProjectInformation(data common.UpdateProjectInformationCommand, username string) error {
	if data.ClusterId == "" {
		return errors.New("Cluster must be provided")
	}

	if data.Project == "" {
		return errors.New("Project name must be provided")
	}

	if data.Billing == "" {
		return errors.New("Accounting number must be provided")
	}

	// Validate permissions
	if err := p.checkAdminPermissions(data.ClusterId, username, data.Project); err != nil {
		return err
	}

	return nil
}

func sendNewProjectMail(clusterId string, projectName string, userName string, megaID string) error {

	mailServer, ok := os.LookupEnv("MAIL_SERVER")
	if !ok {
		return errors.New("Error looking up MAIL_SERVER from environment.")
	}

	fromMail, ok := os.LookupEnv("MAIL_ADMIN_SENDER")
	if !ok {
		return errors.New("Error looking up MAIL_ADMIN_SENDER from environment.")
	}

	newProjectMail, ok := os.LookupEnv("MAIL_NEW_PROJECT_RECIPIENT")
	if !ok {
		return errors.New("Error looking up MAIL_NEW_PROJECT_RECIPIENT from environment.")
	}

	m := gomail.NewMessage()
	m.SetHeader("From", fromMail)

	m.SetHeader("To", newProjectMail)
	m.SetHeader("Subject", fmt.Sprintf("New Project '%v' on OpenShift", projectName))

	m.SetBody("text/html", fmt.Sprintf(`
	Dear Ladys and Gentleman,
	<br><br>
	The following project has been created on:
	<br><br>
	Cluster: %v<br>
	Project name:	%v<br>
	Creator:		%v<br>
	Mega ID:		%v
	<br><br>
	Kind regards<br>
	Your Cloud Team<br>
	IT-OM-SDL-CLP
	`, clusterId, projectName, userName, megaID))

	d := gomail.Dialer{Host: mailServer, Port: 25}
	d.TLSConfig = &tls.Config{InsecureSkipVerify: true}
	err := d.DialAndSend(m)

	if err != nil {
		return err
	}

	return nil
}

func (p Plugin) createNewProject(clusterId string, project string, username string, billing string, megaid string, testProject bool) error {
	project = strings.ToLower(project)
	projectObject := newObjectRequest("ProjectRequest", project)

	resp, err := p.getOseHTTPClient("POST", clusterId, "oapi/v1/projectrequests", bytes.NewReader(projectObject.Bytes()))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusCreated {
		log.Printf("%v created a new project: %v on cluster %v", username, project, clusterId)

		if err := p.changeProjectPermission(clusterId, project, username); err != nil {
			return err
		}

		if err := p.createOrUpdateMetadata(clusterId, project, billing, megaid, username, testProject); err != nil {
			return err
		}
		return nil
	}
	if resp.StatusCode == http.StatusConflict {
		return errors.New("The project already exists")
	}

	errMsg, _ := ioutil.ReadAll(resp.Body)
	log.Println("Error creating new project:", err, resp.StatusCode, string(errMsg))

	return errors.New(genericAPIError)
}

func (p Plugin) changeProjectPermission(clusterId string, project string, username string) error {
	adminRoleBinding, err := p.getAdminRoleBinding(clusterId, project)
	if err != nil {
		return err
	}

	adminRoleBinding.ArrayAppend(strings.ToLower(username), "userNames")
	adminRoleBinding.ArrayAppend(strings.ToUpper(username), "userNames")

	// Update the policyBindings on the api
	resp, err := p.getOseHTTPClient("PUT",
		clusterId,
		"oapi/v1/namespaces/"+project+"/rolebindings/admin",
		bytes.NewReader(adminRoleBinding.Bytes()))
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		log.Print(username + " is now admin of " + project)
		return nil
	}

	errMsg, _ := ioutil.ReadAll(resp.Body)
	log.Println("Error updating project permissions:", err, resp.StatusCode, string(errMsg))
	return errors.New(genericAPIError)
}

type ProjectInformation struct {
	Kontierungsnummer string `json:"kontierungsnummer"`
	MegaID            string `json:"megaid"`
}

func (p Plugin) getProjectInformation(clusterId, project string) (*ProjectInformation, error) {
	resp, err := p.getOseHTTPClient("GET", clusterId, "api/v1/namespaces/"+project, nil)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	json, err := gabs.ParseJSONBuffer(resp.Body)
	if err != nil {
		log.Println("error decoding json:", err, resp.StatusCode)
		return nil, errors.New(genericAPIError)
	}

	billing := json.Path("metadata.annotations").S("openshift.io/kontierung-element").Data()
	if billing == nil {
		billing = ""
	}
	megaid := json.Path("metadata.annotations").S("openshift.io/MEGAID").Data()
	if megaid == nil {
		megaid = ""
	}
	return &ProjectInformation{
		Kontierungsnummer: billing.(string),
		MegaID:            megaid.(string),
	}, nil
}

func (p Plugin) createOrUpdateMetadata(clusterId, project string, billing string, megaid string, username string, testProject bool) error {
	resp, err := p.getOseHTTPClient("GET", clusterId, "api/v1/namespaces/"+project, nil)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	json, err := gabs.ParseJSONBuffer(resp.Body)
	if err != nil {
		log.Println("error decoding json:", err, resp.StatusCode)
		return errors.New(genericAPIError)
	}

	annotations := json.Path("metadata.annotations")
	annotations.Set(billing, "openshift.io/kontierung-element")
	annotations.Set(username, "openshift.io/requester")

	if testProject {
		annotations.Set(testProjectDeletionDays, "openshift.io/testproject-daystodeletion")
		annotations.Set(fmt.Sprintf("Dieses Testprojekt wird in %v Tagen automatisch gelöscht!", testProjectDeletionDays), "openshift.io/description")
	}

	if len(megaid) > 0 {
		annotations.Set(megaid, "openshift.io/MEGAID")
	}

	resp, err = p.getOseHTTPClient("PUT", clusterId, "api/v1/namespaces/"+project, bytes.NewReader(json.Bytes()))
	if err != nil {
		return err
	}

	if resp.StatusCode == http.StatusOK {
		resp.Body.Close()
		log.Println("User "+username+" changed config of project "+project+" on cluster "+clusterId+". Kontierungsnummer: "+billing, ", MegaID: "+megaid)
		return nil
	}

	errMsg, _ := ioutil.ReadAll(resp.Body)
	log.Println("Error updating project config:", err, resp.StatusCode, string(errMsg))

	return errors.New(genericAPIError)
}
