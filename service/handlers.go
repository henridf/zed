package service

import (
	"errors"
	"net/http"
	"time"

	"github.com/brimdata/zed"
	"github.com/brimdata/zed/api"
	"github.com/brimdata/zed/api/queryio"
	"github.com/brimdata/zed/compiler"
	"github.com/brimdata/zed/lake"
	"github.com/brimdata/zed/lake/commits"
	"github.com/brimdata/zed/lake/index"
	"github.com/brimdata/zed/lake/journal"
	"github.com/brimdata/zed/lakeparse"
	"github.com/brimdata/zed/runtime"
	"github.com/brimdata/zed/runtime/op"
	"github.com/brimdata/zed/service/auth"
	"github.com/brimdata/zed/service/srverr"
	"github.com/brimdata/zed/zio"
	"github.com/brimdata/zed/zio/anyio"
)

func handleQuery(c *Core, w *ResponseWriter, r *Request) {
	const queryStatsInterval = time.Second
	var req api.QueryRequest
	if !r.Unmarshal(w, &req) {
		return
	}
	// A note on error handling here.  If we get an error setting up
	// before the query starts to run, we call w.Error() and return
	// an HTTP status error and a JSON formatted error.  If the query
	// begins running then we encounter an error, we return an HTTP
	// status OK (triggered as we start to write to the HTTP response body)
	// and return the error as an embedded ZNG control message.
	// The client must look at the return code and interpret the result
	// accordingly and when it sees a ZNG error after underway,
	// the error should be relay that to the caller/user.
	query, err := compiler.ParseProc(req.Query)
	if err != nil {
		w.Error(srverr.ErrInvalid(err))
		return
	}
	format, err := api.MediaTypeToFormat(r.Header.Get("Accept"), DefaultZedFormat)
	if err != nil {
		w.Error(srverr.ErrInvalid(err))
		return
	}
	flowgraph, err := runtime.NewQueryOnLake(r.Context(), zed.NewContext(), query, c.root, &req.Head, r.Logger)
	if err != nil {
		w.Error(err)
		return
	}
	defer flowgraph.Pull(true)
	flusher, _ := w.ResponseWriter.(http.Flusher)
	writer, err := queryio.NewWriter(zio.NopCloser(w), format, flusher)
	if err != nil {
		w.Error(err)
		return
	}
	// Once we defer writer.Close() are going to write ZNG to the HTTP
	// response body and for errors after this point, we must call
	// writer.WriterError() instead of w.Error().
	defer writer.Close()
	timer := time.NewTicker(queryStatsInterval)
	defer timer.Stop()
	for {
		var tick bool
		select {
		case <-timer.C:
			tick = true
		default:
		}
		batch, err := flowgraph.Pull(false)
		if err != nil {
			if !errors.Is(err, journal.ErrEmpty) {
				writer.WriteError(err)
			}
			return
		}
		if batch == nil || tick {
			if err := writer.WriteProgress(flowgraph.Progress()); err != nil {
				writer.WriteError(err)
				return
			}
			if batch == nil {
				return
			}
		}
		if len(batch.Values()) == 0 {
			if eoc, ok := batch.(*op.EndOfChannel); ok {
				if err := writer.WhiteChannelEnd(int(*eoc)); err != nil {
					writer.WriteError(err)
					return
				}
			}
			continue
		}
		var cid int
		batch, cid = op.Unwrap(batch)
		if err := writer.WriteBatch(cid, batch); err != nil {
			writer.WriteError(err)
			return
		}
	}
}

func handleBranchGet(c *Core, w *ResponseWriter, r *Request) {
	id, ok := r.PoolID(w, c.root)
	if !ok {
		return
	}
	branchName, ok := r.StringFromPath(w, "branch")
	if !ok {
		return
	}
	pool, err := c.root.OpenPool(r.Context(), id)
	if err != nil {
		w.Error(err)
		return
	}
	if branchName != "" {
		branch, err := pool.LookupBranchByName(r.Context(), branchName)
		if err != nil {
			w.Error(err)
			return
		}
		w.Respond(http.StatusOK, api.CommitResponse{Commit: branch.Commit})
		return
	}
	w.Respond(http.StatusOK, pool.Config)
}

func handlePoolStats(c *Core, w *ResponseWriter, r *Request) {
	id, ok := r.PoolID(w, c.root)
	if !ok {
		return
	}
	pool, err := c.root.OpenPool(r.Context(), id)
	if err != nil {
		w.Error(err)
		return
	}
	//XXX app uses this for key range... should handle this differently
	// at minimum on a per-branch basis and app needs to be branch aware
	// If branch not specified, API endpoints here should just assume "main".
	branch, err := pool.OpenBranchByName(r.Context(), "main")
	if err != nil {
		w.Error(err)
		return
	}
	snap, err := branch.Pool().Snapshot(r.Context(), branch.Commit)
	if err != nil {
		if errors.Is(err, journal.ErrEmpty) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Error(err)
		return
	}
	info, err := pool.Stats(r.Context(), snap)
	if err != nil {
		w.Error(err)
		return
	}
	w.Respond(http.StatusOK, info)
}

func handlePoolPost(c *Core, w *ResponseWriter, r *Request) {
	var req api.PoolPostRequest
	if !r.Unmarshal(w, &req) {
		return
	}
	pool, err := c.root.CreatePool(r.Context(), req.Name, req.Layout, req.SeekStride, req.Thresh)
	if err != nil {
		w.Error(err)
		return
	}
	meta, err := pool.Main(r.Context())
	if err != nil {
		w.Error(err)
		return
	}
	w.Respond(http.StatusOK, meta)
	c.publishEvent(w, "pool-new", api.EventPool{PoolID: pool.ID})
}

func handlePoolPut(c *Core, w *ResponseWriter, r *Request) {
	var req api.PoolPutRequest
	if !r.Unmarshal(w, &req) {
		return
	}
	id, ok := r.PoolID(w, c.root)
	if !ok {
		return
	}
	if err := c.root.RenamePool(r.Context(), id, req.Name); err != nil {
		w.Error(err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
	c.publishEvent(w, "pool-update", api.EventPool{PoolID: id})
}

func handleBranchPost(c *Core, w *ResponseWriter, r *Request) {
	var req api.BranchPostRequest
	if !r.Unmarshal(w, &req) {
		return
	}
	poolID, ok := r.PoolID(w, c.root)
	if !ok {
		return
	}
	commit, err := lakeparse.ParseID(req.Commit)
	if err != nil {
		w.Error(srverr.ErrInvalid("invalid commit object: %s", req.Commit))
		return
	}
	branchRef, err := c.root.CreateBranch(r.Context(), poolID, req.Name, commit)
	if err != nil {
		w.Error(err)
		return
	}
	w.Respond(http.StatusOK, branchRef)
	c.publishEvent(w, "branch-update", api.EventBranch{PoolID: poolID, Branch: branchRef.Name})
}

func handleRevertPost(c *Core, w *ResponseWriter, r *Request) {
	poolID, ok := r.PoolID(w, c.root)
	if !ok {
		return
	}
	branch, ok := r.StringFromPath(w, "branch")
	if !ok {
		return
	}
	commit, ok := r.CommitID(w)
	if !ok {
		return
	}
	message, ok := r.decodeCommitMessage(w)
	if !ok {
		return
	}
	commit, err := c.root.Revert(r.Context(), poolID, branch, commit, message.Author, message.Body)
	if err != nil {
		w.Error(err)
		return
	}
	w.Respond(http.StatusOK, api.CommitResponse{Commit: commit})
	c.publishEvent(w, "branch-revert", api.EventBranchCommit{
		CommitID: commit,
		PoolID:   poolID,
		Branch:   branch,
	})
}

func handleBranchMerge(c *Core, w *ResponseWriter, r *Request) {
	poolID, ok := r.PoolID(w, c.root)
	if !ok {
		return
	}
	parentBranch, ok := r.StringFromPath(w, "branch")
	if !ok {
		return
	}
	childBranch, ok := r.StringFromPath(w, "child")
	if !ok {
		return
	}
	message, ok := r.decodeCommitMessage(w)
	if !ok {
		return
	}
	commit, err := c.root.MergeBranch(r.Context(), poolID, childBranch, parentBranch, message.Author, message.Body)
	if err != nil {
		w.Error(err)
		return
	}
	w.Respond(http.StatusOK, api.CommitResponse{Commit: commit})
	c.publishEvent(w, "branch-merge", api.EventBranchCommit{
		CommitID: commit,
		PoolID:   poolID,
		Branch:   childBranch,
		Parent:   parentBranch,
	})
}

func handlePoolDelete(c *Core, w *ResponseWriter, r *Request) {
	id, ok := r.PoolID(w, c.root)
	if !ok {
		return
	}
	if err := c.root.RemovePool(r.Context(), id); err != nil {
		w.Error(err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
	c.publishEvent(w, "pool-delete", api.EventPool{PoolID: id})
}

func handleBranchDelete(c *Core, w *ResponseWriter, r *Request) {
	poolID, ok := r.PoolID(w, c.root)
	if !ok {
		return
	}
	branchName, ok := r.StringFromPath(w, "branch")
	if !ok {
		return
	}
	if err := c.root.RemoveBranch(r.Context(), poolID, branchName); err != nil {
		w.Error(err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
	c.publishEvent(w, "branch-delete", api.EventBranch{PoolID: poolID, Branch: branchName})
}

type warningCollector []string

func (w *warningCollector) Warn(msg string) error {
	*w = append(*w, msg)
	return nil
}

func handleBranchLoad(c *Core, w *ResponseWriter, r *Request) {
	poolID, ok := r.PoolID(w, c.root)
	if !ok {
		return
	}
	branchName, ok := r.StringFromPath(w, "branch")
	if !ok {
		return
	}
	message, ok := r.decodeCommitMessage(w)
	if !ok {
		return
	}
	pool, err := c.root.OpenPool(r.Context(), poolID)
	if err != nil {
		w.Error(err)
		return
	}
	branch, err := pool.OpenBranchByName(r.Context(), branchName)
	if err != nil {
		w.Error(err)
		return
	}
	// Force validation of ZNG when initialing loading into the lake.
	var opts anyio.ReaderOpts
	opts.ZNG.Validate = true
	zctx := zed.NewContext()
	zrc, err := anyio.NewReaderWithOpts(anyio.GzipReader(r.Body), zctx, opts)
	if err != nil {
		w.Error(srverr.ErrInvalid(err))
		return
	}
	defer zrc.Close()
	warnings := warningCollector{}
	wr := zio.NewWarningReader(zrc, &warnings)
	kommit, err := branch.Load(r.Context(), zctx, wr, message.Author, message.Body, message.Meta)
	if err != nil {
		if errors.Is(err, commits.ErrEmptyTransaction) {
			err = srverr.ErrInvalid("no records in request")
		}
		if errors.Is(err, lake.ErrInvalidCommitMeta) {
			err = srverr.ErrInvalid("invalid commit metadata in request")
		}
		w.Error(err)
		return
	}
	w.Respond(http.StatusOK, api.CommitResponse{
		Warnings: warnings,
		Commit:   kommit,
	})
	c.publishEvent(w, "branch-commit", api.EventBranchCommit{
		CommitID: kommit,
		PoolID:   pool.ID,
		Branch:   branch.Name,
	})
}

func handleDelete(c *Core, w *ResponseWriter, r *Request) {
	poolID, ok := r.PoolID(w, c.root)
	if !ok {
		return
	}
	branchName, ok := r.StringFromPath(w, "branch")
	if !ok {
		return
	}
	message, ok := r.decodeCommitMessage(w)
	if !ok {
		return
	}
	var payload api.DeleteRequest
	if !r.Unmarshal(w, &payload) {
		return
	}
	pool, err := c.root.OpenPool(r.Context(), poolID)
	if err != nil {
		w.Error(err)
		return
	}
	branch, err := pool.OpenBranchByName(r.Context(), branchName)
	if err != nil {
		w.Error(err)
		return
	}
	ids, err := branch.LookupTags(r.Context(), payload.ObjectIDs)
	if err != nil {
		w.Error(err)
		return
	}
	commit, err := branch.Delete(r.Context(), ids, message.Author, message.Body)
	if err != nil {
		w.Error(err)
		return
	}
	w.Marshal(api.CommitResponse{Commit: commit})
}

func handleIndexRulesPost(c *Core, w *ResponseWriter, r *Request) {
	var body api.IndexRulesAddRequest
	if !r.Unmarshal(w, &body, index.RuleTypes...) {
		return
	}
	if err := c.root.AddIndexRules(r.Context(), body.Rules); err != nil {
		w.Error(err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func handleIndexRulesDelete(c *Core, w *ResponseWriter, r *Request) {
	var req api.IndexRulesDeleteRequest
	if !r.Unmarshal(w, &req) {
		return
	}
	ruleIDs, err := lakeparse.ParseIDs(req.RuleIDs)
	if err != nil {
		w.Error(srverr.ErrInvalid(err))
	}
	rules, err := c.root.DeleteIndexRules(r.Context(), ruleIDs)
	if err != nil {
		w.Error(err)
		return
	}
	w.Respond(http.StatusOK, api.IndexRulesDeleteResponse{Rules: rules})
}

func handleIndexApply(c *Core, w *ResponseWriter, r *Request, branch *lake.Branch) {
	var req api.IndexApplyRequest
	if !r.Unmarshal(w, &req) {
		return
	}
	tags, err := branch.LookupTags(r.Context(), req.Tags)
	if err != nil {
		w.Error(err)
		return
	}
	rules, err := c.root.LookupIndexRules(r.Context(), req.RuleName)
	if err != nil {
		w.Error(err)
		return
	}
	commit, err := branch.ApplyIndexRules(r.Context(), rules, tags)
	if err != nil {
		w.Error(err)
		return
	}
	w.Respond(http.StatusOK, api.CommitResponse{Commit: commit})

}

func handleIndexUpdate(c *Core, w *ResponseWriter, r *Request, branch *lake.Branch) {
	var req api.IndexUpdateRequest
	if !r.Unmarshal(w, &req) {
		return
	}
	var err error
	var rules []index.Rule
	if len(req.RuleNames) > 0 {
		rules, err = c.root.LookupIndexRules(r.Context(), req.RuleNames...)
	} else {
		rules, err = c.root.AllIndexRules(r.Context())
	}
	if err != nil {
		w.Error(err)
		return
	}
	commit, err := branch.UpdateIndex(r.Context(), rules)
	if err != nil {
		if errors.Is(err, commits.ErrEmptyTransaction) {
			err = srverr.ErrInvalid(err)
		}
		w.Error(err)
		return
	}
	w.Respond(http.StatusOK, api.CommitResponse{Commit: commit})
}

func handleAuthIdentityGet(c *Core, w *ResponseWriter, r *Request) {
	ident := auth.IdentityFromContext(r.Context())
	w.Respond(http.StatusOK, api.AuthIdentityResponse{
		TenantID: string(ident.TenantID),
		UserID:   string(ident.UserID),
	})
}

func handleAuthMethodGet(c *Core, w *ResponseWriter, r *Request) {
	if c.auth == nil {
		w.Respond(http.StatusOK, api.AuthMethodResponse{Kind: api.AuthMethodNone})
		return
	}
	w.Respond(http.StatusOK, c.auth.MethodResponse())
}

func handleEvents(c *Core, w *ResponseWriter, r *Request) {
	format, err := api.MediaTypeToFormat(r.Header.Get("Accept"), "zson")
	if err != nil {
		w.Error(srverr.ErrInvalid(err))
	}
	w.Header().Set("Content-Type", "text/event-stream")
	writer := &eventStreamWriter{body: w, format: format}
	subscription := make(chan event)
	c.subscriptionsMu.Lock()
	c.subscriptions[subscription] = struct{}{}
	c.subscriptionsMu.Unlock()
	for {
		select {
		case ev := <-subscription:
			if err := writer.writeEvent(ev); err != nil {
				w.Error(err)
				continue
			}
			if f, ok := w.ResponseWriter.(http.Flusher); ok {
				f.Flush()
			}
		case <-r.Context().Done():
			c.subscriptionsMu.Lock()
			delete(c.subscriptions, subscription)
			c.subscriptionsMu.Unlock()
			return
		}
	}
}
