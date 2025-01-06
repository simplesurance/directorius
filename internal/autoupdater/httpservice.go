package autoupdater

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strconv"

	_ "embed" // used to embed html templates and static docs

	"github.com/simplesurance/directorius/internal/autoupdater/pages/types"
	"github.com/simplesurance/directorius/internal/logfields"

	"go.uber.org/zap"
)

// templFS contains the web pages.
//
//go:embed pages/templates/*
var templFS embed.FS

//go:embed pages/static/*
var staticFS embed.FS

var templFuncs = template.FuncMap{
	"add": func(a, b int) int {
		return a + b
	},
}

// these bounds are only used in the webinterface, all int32 values  work as
// priorities
const (
	prPriorityMax int = 2
	prPriorityMin int = -1
)

const (
	handlerPriorityUpdatePath = "priority_post"
	handlerSuspendResumePath  = "suspendresume_post"
)

type HTTPService struct {
	autoupdater *Autoupdater
	templates   *template.Template
	logger      *zap.Logger
	basepath    string
}

func NewHTTPService(autoupdater *Autoupdater, endpoint string) *HTTPService {
	return &HTTPService{
		autoupdater: autoupdater,
		templates: template.Must(
			template.New("").
				Funcs(templFuncs).
				ParseFS(templFS, "pages/templates/*"),
		),
		basepath: endpoint,
		logger:   autoupdater.Logger.Named("http_service"),
	}
}

func (h *HTTPService) RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET "+h.basepath+"{$}", h.HandlerListFunc)
	mux.HandleFunc("POST "+path.Join(h.basepath, handlerPriorityUpdatePath),
		h.HandlerPriorityUpdate)
	mux.HandleFunc("POST "+path.Join(h.basepath, handlerSuspendResumePath),
		h.HandlerSuspendResume)

	staticPath := h.basepath + "static" + "/"
	mux.Handle(
		staticPath,
		http.StripPrefix(
			staticPath,
			h.HandlerStaticFiles(),
		),
	)
}

func (h *HTTPService) HandlerPriorityUpdate(resp http.ResponseWriter, req *http.Request) {
	if err := req.ParseForm(); err != nil {
		h.logger.Debug("parsing post-data failed",
			zap.Error(err), logfields.HTTPRequestURL(req.RequestURI),
		)

		http.Error(resp, "could not parse form data", http.StatusBadRequest)
		return
	}

	updates, err := formToPriorityUpdates(req.Form)
	if err != nil {
		http.Error(resp, "parsing form data failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	h.autoupdater.SetPullRequestPriorities(updates)

	http.Redirect(resp, req, h.basepath, http.StatusSeeOther)
}

func (h *HTTPService) HandlerSuspendResume(resp http.ResponseWriter, req *http.Request) {
	const kAction = "action"

	if err := req.ParseForm(); err != nil {
		h.logger.Debug("parsing post-data failed",
			zap.Error(err), logfields.HTTPRequestURL(req.RequestURI),
		)

		http.Error(resp, "could not parse form data", http.StatusBadRequest)
		return
	}

	branchID, err := formToBranchID(req.Form)
	if err != nil {
		http.Error(resp, "parsing form data failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	switch a := req.Form.Get(kAction); a {
	case "resume":
		h.autoupdater.ResumeQueue(branchID)
	case "pause":
		h.autoupdater.PauseQueue(branchID)

	case "":
		http.Error(resp,
			fmt.Sprintf("parsing form data failed: %q value is missing or empty", kAction),
			http.StatusBadRequest,
		)
		return
	default:
		http.Error(resp,
			fmt.Sprintf("parsing form data failed: unexpected %q value %q", kAction, a),
			http.StatusBadRequest)

		return
	}

	http.Redirect(resp, req, h.basepath, http.StatusSeeOther)
}

func formToBranchID(form url.Values) (*BranchID, error) {
	const (
		kRepo       = "repository"
		kRepoOwner  = "repository_owner"
		kBaseBranch = "base_branch"
	)

	repo := form.Get(kRepo)
	if repo == "" {
		return nil, fmt.Errorf("%q value is missing or empty", kRepo)
	}

	repoOwner := form.Get(kRepoOwner)
	if repoOwner == "" {
		return nil, fmt.Errorf("%q value is missing or empty", kRepoOwner)
	}

	baseBranch := form.Get(kBaseBranch)
	if baseBranch == "" {
		return nil, fmt.Errorf("%q value is missing or empty", kBaseBranch)
	}

	return &BranchID{
		RepositoryOwner: repoOwner,
		Repository:      repo,
		Branch:          baseBranch,
	}, nil
}

func formToPriorityUpdates(form url.Values) (*PRPriorityUpdates, error) {
	result := PRPriorityUpdates{
		Updates: make([]*PRPriorityUpdate, 0, len(form)),
	}

	for k, vals := range form {
		if len(vals) != 1 {
			return nil, fmt.Errorf("form value of %s contains %d values (%+v), expecting 1", k, len(vals), vals)
		}

		if k == "basebranch" {
			result.BranchID.Branch = vals[0]
			continue
		}
		if k == "owner" {
			result.BranchID.RepositoryOwner = vals[0]
			continue
		}
		if k == "repository" {
			result.BranchID.Repository = vals[0]
			continue
		}

		prNrInt, err := strconv.Atoi(k)
		if err != nil {
			return nil, fmt.Errorf("form key %q is not a pull request number", k)
		}
		val := vals[0]
		if val == types.OptionValueSelected {
			// priority has not been modified
			continue
		}

		priority, err := strconv.ParseInt(val, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("parsing priority value (%q) as int32 for pr %s failed: %w", val, k, err)
		}

		if priority < int64(prPriorityMin) {
			return nil, fmt.Errorf("priority value (%q) for pr %s is smaller than %d", val, k, prPriorityMin)
		}
		if priority > int64(prPriorityMax) {
			return nil, fmt.Errorf("priority value (%q) for pr %s is bigger than %d", val, k, prPriorityMax)
		}

		result.Updates = append(result.Updates, &PRPriorityUpdate{
			PRNumber: prNrInt,
			Priority: int32(priority),
		})
	}

	return &result, nil
}

func (h *HTTPService) HandlerStaticFiles() http.Handler {
	subFs, err := fs.Sub(staticFS, "pages/static")
	if err != nil {
		h.logger.Panic("creating sub fs for static http files failed", zap.Error(err))
	}

	return http.FileServer(http.FS(subFs))
}

func (h *HTTPService) HandlerListFunc(respWr http.ResponseWriter, _ *http.Request) {
	data := h.autoupdater.httpListData()

	err := h.templates.ExecuteTemplate(respWr, "list.html.tmpl", data)
	if err != nil {
		h.logger.Info("applying template and sending it back as http response failed", zap.Error(err))
		http.Error(respWr, err.Error(), http.StatusInternalServerError)
		return
	}
}
