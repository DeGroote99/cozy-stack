package sharings_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cozy/cozy-stack/pkg/couchdb"
	"github.com/cozy/cozy-stack/pkg/instance"
	"github.com/cozy/cozy-stack/pkg/sharing"
	"github.com/cozy/echo"
	uuid "github.com/satori/go.uuid"
	"github.com/stretchr/testify/assert"
)

// Things for the replicator tests
var tsR *httptest.Server
var replInstance *instance.Instance
var replSharingID, replAccessToken string
var fileSharingID, fileAccessToken string

const replDoctype = "io.cozy.replicator.tests"

// It's not really a test, more a setup for the replicator tests
func TestCreateSharingForReplicatorTest(t *testing.T) {
	rule := sharing.Rule{
		Title:    "tests",
		DocType:  replDoctype,
		Selector: "foo",
		Values:   []string{"bar", "baz"},
		Add:      "sync",
		Update:   "sync",
		Remove:   "sync",
	}
	s := sharing.Sharing{
		Description: "replicator tests",
		Rules:       []sharing.Rule{rule},
	}
	assert.NoError(t, s.BeOwner(replInstance, ""))
	s.Members = append(s.Members, sharing.Member{
		Status:   sharing.MemberStatusReady,
		Name:     "J. Doe",
		Email:    "j.doe@example.net",
		Instance: "https://j.example.net/",
	})
	s.Credentials = append(s.Credentials, sharing.Credentials{})
	_, err := s.Create(replInstance)
	assert.NoError(t, err)
	replSharingID = s.SID

	cli, err := sharing.CreateOAuthClient(replInstance, &s.Members[1])
	assert.NoError(t, err)
	s.Credentials[0].Client = sharing.ConvertOAuthClient(cli)
	token, err := sharing.CreateAccessToken(replInstance, cli, s.SID)
	assert.NoError(t, err)
	s.Credentials[0].AccessToken = token
	assert.NoError(t, couchdb.UpdateDoc(replInstance, &s))
	replAccessToken = token.AccessToken
	assert.NoError(t, couchdb.CreateDB(replInstance, replDoctype))
}

func uuidv4() string {
	id, _ := uuid.NewV4()
	return id.String()
}

func createShared(t *testing.T, sid string, revisions []string) *sharing.SharedRef {
	rev := fmt.Sprintf("%d-%s", len(revisions), revisions[0])
	parts := strings.SplitN(sid, "/", 2)
	doctype := parts[0]
	id := parts[1]
	docs := []map[string]interface{}{
		{
			"_id":  id,
			"_rev": rev,
			"_revisions": map[string]interface{}{
				"start": sharing.RevGeneration(rev),
				"ids":   revisions,
			},
			"this": "is document " + id + " at revision " + rev,
		},
	}
	err := couchdb.BulkForceUpdateDocs(replInstance, doctype, docs)
	assert.NoError(t, err)
	revs := make([]string, len(revisions))
	for i := range revisions {
		revs[i] = fmt.Sprintf("%d-%s", i+1, revisions[len(revisions)-i-1])
	}
	ref := sharing.SharedRef{
		SID:       sid,
		Revisions: revs,
		Infos: map[string]sharing.SharedInfo{
			replSharingID: {Rule: 0},
		},
	}
	err = couchdb.CreateNamedDocWithDB(replInstance, &ref)
	assert.NoError(t, err)
	return &ref
}

func TestPermissions(t *testing.T) {
	assert.NotNil(t, replSharingID)
	assert.NotNil(t, replAccessToken)

	id := replDoctype + "/" + uuidv4()
	doc := createShared(t, id, []string{"111111111"})

	body, _ := json.Marshal(sharing.Changes{
		"id": doc.Revisions,
	})
	u := tsR.URL + "/sharings/" + replSharingID + "/_revs_diff"

	r := bytes.NewReader(body)
	req, err := http.NewRequest(http.MethodPost, u, r)
	assert.NoError(t, err)
	req.Header.Add(echo.HeaderAccept, "application/json")
	req.Header.Add(echo.HeaderContentType, "application/json")
	res, err := http.DefaultClient.Do(req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, res.StatusCode)
	defer res.Body.Close()

	r = bytes.NewReader(body)
	req, err = http.NewRequest(http.MethodPost, u, r)
	assert.NoError(t, err)
	req.Header.Add(echo.HeaderAccept, "application/json")
	req.Header.Add(echo.HeaderContentType, "application/json")
	req.Header.Add(echo.HeaderAuthorization, "Bearer "+replAccessToken)
	res, err = http.DefaultClient.Do(req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, res.StatusCode)
	defer res.Body.Close()
}

func TestRevsDiff(t *testing.T) {
	assert.NotEmpty(t, replSharingID)
	assert.NotEmpty(t, replAccessToken)

	sid1 := replDoctype + "/" + uuidv4()
	createShared(t, sid1, []string{"1a", "1a", "1a"})
	sid2 := replDoctype + "/" + uuidv4()
	createShared(t, sid2, []string{"2a", "2a", "2a"})
	sid3 := replDoctype + "/" + uuidv4()
	createShared(t, sid3, []string{"3a", "3a", "3a"})
	sid4 := replDoctype + "/" + uuidv4()
	createShared(t, sid4, []string{"4a", "4a", "4a"})
	sid5 := replDoctype + "/" + uuidv4()
	createShared(t, sid5, []string{"5a", "5a", "5a"})
	sid6 := replDoctype + "/" + uuidv4()

	body, _ := json.Marshal(sharing.Changes{
		sid1: []string{"3-1a"},
		sid2: []string{"2-2a"},
		sid3: []string{"5-3b"},
		sid4: []string{"2-4b", "2-4c", "4-4d"},
		sid6: []string{"1-6b"},
	})
	r := bytes.NewReader(body)
	u := tsR.URL + "/sharings/" + replSharingID + "/_revs_diff"
	req, err := http.NewRequest(http.MethodPost, u, r)
	assert.NoError(t, err)
	req.Header.Add(echo.HeaderAccept, "application/json")
	req.Header.Add(echo.HeaderContentType, "application/json")
	req.Header.Add(echo.HeaderAuthorization, "Bearer "+replAccessToken)
	res, err := http.DefaultClient.Do(req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, res.StatusCode)
	defer res.Body.Close()

	missings := make(sharing.Missings)
	err = json.NewDecoder(res.Body).Decode(&missings)
	assert.NoError(t, err)

	// sid1 is the same on both sides
	assert.NotContains(t, missings, sid1)

	// sid2 was updated on the target
	assert.NotContains(t, missings, sid2)

	// sid3 was updated on the source
	assert.Contains(t, missings, sid3)
	assert.Equal(t, missings[sid3].Missing, []string{"5-3b"})
	assert.Equal(t, missings[sid3].PAs, []string{"3-3a"})

	// sid4 is a conflict
	assert.Contains(t, missings, sid4)
	assert.Equal(t, missings[sid4].Missing, []string{"2-4b", "2-4c", "4-4d"})
	assert.Equal(t, missings[sid4].PAs, []string{"1-4a", "3-4a"})

	// sid5 has been created on the target
	assert.NotContains(t, missings, sid5)

	// sid6 has been created on the source
	assert.Contains(t, missings, sid6)
	assert.Equal(t, missings[sid6].Missing, []string{"1-6b"})
	assert.Empty(t, missings[sid6].PAs)
}

func assertSharedDoc(t *testing.T, sid, rev string) {
	parts := strings.SplitN(sid, "/", 2)
	doctype := parts[0]
	id := parts[1]
	var doc couchdb.JSONDoc
	assert.NoError(t, couchdb.GetDoc(replInstance, doctype, id, &doc))
	assert.Equal(t, doc.ID(), id)
	assert.Equal(t, doc.Rev(), rev)
	assert.Equal(t, doc.M["this"], "is document "+id+" at revision "+rev)
}

func TestBulkDocs(t *testing.T) {
	assert.NotEmpty(t, replSharingID)
	assert.NotEmpty(t, replAccessToken)

	id1 := uuidv4()
	sid1 := replDoctype + "/" + id1
	createShared(t, sid1, []string{"aaa", "bbb"})
	id2 := uuidv4()
	sid2 := replDoctype + "/" + id2

	body, _ := json.Marshal(sharing.DocsByDoctype{
		replDoctype: {
			{
				"_id":  id1,
				"_rev": "3-ccc",
				"_revisions": map[string]interface{}{
					"start": 3,
					"ids":   []string{"ccc", "bbb"},
				},
				"this": "is document " + id1 + " at revision 3-ccc",
				"foo":  "bar",
			},
			{
				"_id":  id2,
				"_rev": "3-fff",
				"_revisions": map[string]interface{}{
					"start": 3,
					"ids":   []string{"fff", "eee", "dd"},
				},
				"this": "is document " + id2 + " at revision 3-fff",
				"foo":  "baz",
			},
		},
	})
	r := bytes.NewReader(body)
	u := tsR.URL + "/sharings/" + replSharingID + "/_bulk_docs"
	req, err := http.NewRequest(http.MethodPost, u, r)
	assert.NoError(t, err)
	req.Header.Add(echo.HeaderAccept, "application/json")
	req.Header.Add(echo.HeaderContentType, "application/json")
	req.Header.Add(echo.HeaderAuthorization, "Bearer "+replAccessToken)
	res, err := http.DefaultClient.Do(req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, res.StatusCode)
	defer res.Body.Close()

	assertSharedDoc(t, sid1, "3-ccc")
	assertSharedDoc(t, sid2, "3-fff")
}

// It's not really a test, more a setup for the io.cozy.files tests
func TestCreateSharingForUploadFileTest(t *testing.T) {
	dirID := uuidv4()
	ruleOne := sharing.Rule{
		Title:    "file one",
		DocType:  "io.cozy.files",
		Selector: "",
		Values:   []string{dirID},
		Add:      "push",
		Update:   "push",
		Remove:   "push",
	}
	s := sharing.Sharing{
		Description: "upload files tests",
		Rules:       []sharing.Rule{ruleOne},
	}
	assert.NoError(t, s.BeOwner(replInstance, ""))
	s.Members = append(s.Members, sharing.Member{
		Status:   sharing.MemberStatusReady,
		Name:     "J. Doe",
		Email:    "j.doe@example.net",
		Instance: "https://j.example.net/",
	})
	s.Credentials = append(s.Credentials, sharing.Credentials{})
	_, err := s.Create(replInstance)
	assert.NoError(t, err)
	fileSharingID = s.SID

	assert.NoError(t, s.CreateDirForSharing(replInstance, &s.Rules[0]))

	cli, err := sharing.CreateOAuthClient(replInstance, &s.Members[1])
	assert.NoError(t, err)
	s.Credentials[0].Client = sharing.ConvertOAuthClient(cli)
	token, err := sharing.CreateAccessToken(replInstance, cli, s.SID)
	assert.NoError(t, err)
	s.Credentials[0].AccessToken = token
	assert.NoError(t, couchdb.UpdateDoc(replInstance, &s))
	fileAccessToken = token.AccessToken
}

func TestUploadNewFile(t *testing.T) {
	assert.NotEmpty(t, fileSharingID)
	assert.NotEmpty(t, fileAccessToken)

	fileOneID := uuidv4()
	body, _ := json.Marshal(map[string]interface{}{
		"_id":  fileOneID,
		"_rev": "1-5f9ba207fefdc250e35f7cd866c84cc6",
		"_revisions": map[string]interface{}{
			"start": 1,
			"ids":   []string{"5f9ba207fefdc250e35f7cd866c84cc6"},
		},
		"type":       "file",
		"name":       "hello.txt",
		"created_at": "2018-04-23T18:11:42.343937292+02:00",
		"updated_at": "2018-04-23T18:11:42.343937292+02:00",
		"size":       "6",
		"md5sum":     "WReFt5RgHiErJg4lklY2/Q==",
		"mime":       "text/plain",
		"class":      "text",
		"executable": false,
		"trashed":    false,
		"tags":       []string{},
	})
	r := bytes.NewReader(body)
	u := tsR.URL + "/sharings/" + fileSharingID + "/io.cozy.files/" + fileOneID + "/metadata"
	req, err := http.NewRequest(http.MethodPut, u, r)
	assert.NoError(t, err)
	req.Header.Add(echo.HeaderAccept, "application/json")
	req.Header.Add(echo.HeaderContentType, "application/json")
	req.Header.Add(echo.HeaderAuthorization, "Bearer "+fileAccessToken)
	res, err := http.DefaultClient.Do(req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, res.StatusCode)
	defer res.Body.Close()
	var key map[string]string
	assert.NoError(t, json.NewDecoder(res.Body).Decode(&key))
	assert.NotEmpty(t, key["key"])

	r2 := strings.NewReader("world\n")
	u2 := tsR.URL + "/sharings/" + fileSharingID + "/io.cozy.files/" + key["key"]
	req2, err := http.NewRequest(http.MethodPut, u2, r2)
	assert.NoError(t, err)
	req2.Header.Add(echo.HeaderContentType, "text/plain")
	req2.Header.Add(echo.HeaderAuthorization, "Bearer "+fileAccessToken)
	res2, err := http.DefaultClient.Do(req2)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, res2.StatusCode)
	defer res.Body.Close()
}
