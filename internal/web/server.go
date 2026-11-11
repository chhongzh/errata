package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/chhongzh/errata/internal/assets"
	"github.com/chhongzh/errata/internal/derive"
	"github.com/chhongzh/errata/internal/gitx"
	"github.com/chhongzh/errata/internal/render"
	"github.com/chhongzh/errata/internal/store"
	"github.com/chhongzh/errata/internal/syncer"
)

const maxUpload = 10 << 20

var imageTypes = map[string]string{
	"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp",
}

// Server is the local editor. Every request reopens the repo so edits made
// through the CLI show up without a restart.
type Server struct {
	Root string
	mu   sync.Mutex
}

func New(root string) *Server { return &Server{Root: root} }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/meta", s.api(s.meta))
	mux.HandleFunc("GET /api/problems", s.api(s.list))
	mux.HandleFunc("GET /api/problems/{id}", s.api(s.get))
	mux.HandleFunc("POST /api/problems", s.api(s.create))
	mux.HandleFunc("PUT /api/problems/{id}", s.api(s.update))
	mux.HandleFunc("DELETE /api/problems/{id}", s.api(s.remove))
	mux.HandleFunc("POST /api/preview", s.api(s.preview))
	mux.HandleFunc("POST /api/upload", s.api(s.upload))
	mux.HandleFunc("POST /api/subjects", s.api(s.addSubject))
	mux.HandleFunc("POST /api/types", s.api(s.addType))
	mux.HandleFunc("GET /api/status", s.api(s.status))
	mux.HandleFunc("POST /api/sync", s.api(s.sync))
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(filepath.Join(s.Root, store.AssetsDir)))))

	sub, _ := fs.Sub(assets.Admin, "admin")
	mux.Handle("GET /", http.FileServerFS(sub))
	return guard(mux)
}

// guard rejects requests that a malicious web page could aim at localhost:
// DNS-rebinding (unexpected Host) and cross-site writes (foreign Origin,
// missing custom header).
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "localhost" && host != "127.0.0.1" && host != "[::1]" && host != "::1" {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get("X-Errata") != "1" {
				http.Error(w, "missing X-Errata header", http.StatusForbidden)
				return
			}
			if o := r.Header.Get("Origin"); o != "" {
				u, err := url.Parse(o)
				if err != nil || u.Host != r.Host {
					http.Error(w, "cross-origin request refused", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

type apiError struct {
	Status int
	Msg    string
	Log    []string
}

func (e *apiError) Error() string { return e.Msg }

func bad(format string, a ...any) *apiError {
	return &apiError{Status: http.StatusBadRequest, Msg: fmt.Sprintf(format, a...)}
}

type handler func(r *http.Request, repo *store.Repo) (any, error)

func (s *Server) api(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		repo, err := store.Open(s.Root)
		var out any
		if err == nil {
			out, err = h(req, repo)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err != nil {
			e := &apiError{Status: http.StatusInternalServerError, Msg: err.Error()}
			var ae *apiError
			if errors.As(err, &ae) {
				e = ae
			}
			w.WriteHeader(e.Status)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": e.Msg, "log": e.Log})
			return
		}
		_ = json.NewEncoder(w).Encode(out)
	}
}

func decode(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, maxUpload)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return bad("请求体不是合法 JSON: %v", err)
	}
	return nil
}

func number(r *http.Request) (int, error) {
	n, err := store.ParseID(r.PathValue("id"))
	if err != nil {
		return 0, bad("%v", err)
	}
	return n, nil
}

func (s *Server) meta(_ *http.Request, repo *store.Repo) (any, error) {
	ps, err := repo.LoadAll()
	if err != nil {
		return nil, err
	}
	tags := store.Tags(ps)
	return map[string]any{
		"title":    repo.Cfg.Title,
		"subjects": repo.Cfg.Subjects,
		"types":    repo.Cfg.Types,
		"tags":     tags,
		"total":    len(ps),
	}, nil
}

type summary struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Subject string   `json:"subject"`
	Type    string   `json:"type"`
	Tags    []string `json:"tags"`
	Created string   `json:"created"`
}

func (s *Server) list(_ *http.Request, repo *store.Repo) (any, error) {
	ps, err := repo.LoadAll()
	if err != nil {
		return nil, err
	}
	out := make([]summary, 0, len(ps))
	for i := len(ps) - 1; i >= 0; i-- {
		p := ps[i]
		tags := p.Meta.Tags
		if tags == nil {
			tags = []string{}
		}
		out = append(out, summary{p.ID, p.Meta.Title, p.Meta.Subject, p.Meta.Type, tags, p.Meta.Created})
	}
	return out, nil
}

func (s *Server) get(r *http.Request, repo *store.Repo) (any, error) {
	n, err := number(r)
	if err != nil {
		return nil, err
	}
	p, err := repo.Load(n)
	if errors.Is(err, os.ErrNotExist) {
		return nil, &apiError{Status: http.StatusNotFound, Msg: store.FormatID(n) + " 不存在"}
	}
	if err != nil {
		return nil, err
	}
	return p.Doc(), nil
}

func (s *Server) save(repo *store.Repo, d store.Doc, n int) (any, error) {
	p := d.Problem()
	p.ID = store.FormatID(n)
	if old, err := repo.Load(n); err == nil {
		p.Meta.Created = old.Meta.Created
	}
	if errs := repo.Normalize(p); len(errs) > 0 {
		return nil, &apiError{Status: http.StatusUnprocessableEntity, Msg: "校验失败", Log: errs}
	}
	if err := repo.Commit(p); err != nil {
		return nil, err
	}
	_ = os.Remove(repo.StagingPath(n))
	if err := derive.Build(repo); err != nil {
		return nil, fmt.Errorf("已保存，但重新生成派生文件失败: %w", err)
	}
	return p.Doc(), nil
}

func (s *Server) create(r *http.Request, repo *store.Repo) (any, error) {
	var d store.Doc
	if err := decode(r, &d); err != nil {
		return nil, err
	}
	n, err := repo.NextNumber()
	if err != nil {
		return nil, err
	}
	return s.save(repo, d, n)
}

func (s *Server) update(r *http.Request, repo *store.Repo) (any, error) {
	n, err := number(r)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(repo.ProblemPath(n)); err != nil {
		return nil, &apiError{Status: http.StatusNotFound, Msg: store.FormatID(n) + " 不存在"}
	}
	var d store.Doc
	if err := decode(r, &d); err != nil {
		return nil, err
	}
	return s.save(repo, d, n)
}

func (s *Server) remove(r *http.Request, repo *store.Repo) (any, error) {
	n, err := number(r)
	if err != nil {
		return nil, err
	}
	ok, err := repo.Remove(n)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &apiError{Status: http.StatusNotFound, Msg: store.FormatID(n) + " 不存在"}
	}
	if err := derive.Build(repo); err != nil {
		return nil, err
	}
	return map[string]string{"deleted": store.FormatID(n)}, nil
}

func (s *Server) preview(r *http.Request, _ *store.Repo) (any, error) {
	var in struct {
		Text string `json:"text"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	h, err := render.Markdown(in.Text)
	if err != nil {
		return nil, err
	}
	h = strings.ReplaceAll(h, `src="../assets/`, `src="/assets/`)
	return map[string]string{"html": h}, nil
}

func (s *Server) upload(r *http.Request, repo *store.Repo) (any, error) {
	ext, ok := imageTypes[strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))]
	if !ok {
		return nil, bad("只支持 png / jpeg / gif / webp 图片")
	}
	r.Body = http.MaxBytesReader(nil, r.Body, maxUpload)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, bad("读取图片失败（上限 %d MB）: %v", maxUpload>>20, err)
	}
	if len(data) == 0 {
		return nil, bad("图片为空")
	}
	sum := sha256.Sum256(data)
	name := hex.EncodeToString(sum[:])[:12] + ext
	dir := repo.Path(store.AssetsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		return nil, err
	}
	return map[string]string{"path": "../" + store.AssetsDir + "/" + name}, nil
}

type namedIn struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
}

func addNamed(repo *store.Repo, list *[]store.Named, in namedIn) error {
	if !store.ValidSlug(in.ID) {
		return bad("id %q 必须是小写英文 kebab-case", in.ID)
	}
	if strings.TrimSpace(in.Name) == "" {
		return bad("name 不能为空")
	}
	if store.HasNamed(*list, in.ID) || store.HasNamed(*list, in.Name) {
		return bad("%q 已存在", in.ID)
	}
	*list = append(*list, store.Named{ID: in.ID, Name: strings.TrimSpace(in.Name), Aliases: in.Aliases})
	return store.SaveConfig(repo.Root, repo.Cfg)
}

func (s *Server) addSubject(r *http.Request, repo *store.Repo) (any, error) {
	var in namedIn
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if err := addNamed(repo, &repo.Cfg.Subjects, in); err != nil {
		return nil, err
	}
	return repo.Cfg.Subjects, nil
}

func (s *Server) addType(r *http.Request, repo *store.Repo) (any, error) {
	var in namedIn
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if err := addNamed(repo, &repo.Cfg.Types, in); err != nil {
		return nil, err
	}
	return repo.Cfg.Types, nil
}

func (s *Server) status(_ *http.Request, repo *store.Repo) (any, error) {
	out := map[string]any{"dirty": []string{}, "remote": "", "branch": ""}
	g, err := gitx.Open(repo.Root)
	if err != nil {
		return out, nil
	}
	if d, err := g.Dirty(); err == nil && d != nil {
		out["dirty"] = d
	}
	out["remote"] = g.RemoteURL()
	out["branch"] = g.Branch()
	return out, nil
}

func (s *Server) sync(_ *http.Request, repo *store.Repo) (any, error) {
	res, err := syncer.Sync(repo, syncer.Options{})
	var log []string
	if res != nil {
		log = res.Log
	}
	if err != nil {
		return nil, &apiError{Status: http.StatusBadGateway, Msg: err.Error(), Log: log}
	}
	return map[string]any{"log": log}, nil
}
