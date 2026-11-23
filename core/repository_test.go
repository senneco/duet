package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/senneco/duet/vcs"
)

func repositoryReview(t *testing.T) (*Core, string, string, string) {
	t.Helper()
	root := baseRepo(t)
	p := baseWrite(t, root, "a.md", "committed\n")
	baseCommit(t, root)
	baseWrite(t, root, "a.md", "review\n")
	c, err := Open(t.TempDir())
	must(t, err)
	id := create(t, c, Submission{Path: p, Content: "review\n", Original: orig("wrong\n")})
	_, err = c.PrepareWeb(Session{ID: "s", Cwd: root}, id, []string{p})
	must(t, err)
	return c, id, root, p
}
func hasProblem(v ReviewView, code, path string) bool {
	if v.Completeness == nil {
		return false
	}
	for _, p := range v.Completeness.Problems {
		if p.Code == code && p.Path == path {
			return true
		}
	}
	return false
}
func noAccepted(t *testing.T, c *Core, id string) {
	t.Helper()
	ns, err := c.TakeNotices(id)
	must(t, err)
	for _, n := range ns {
		if n.Reason == NoticeAccepted {
			t.Fatalf("false acceptance: %+v", n)
		}
	}
	if rv(t, c, id).Accepted {
		t.Fatal("incomplete review accepted")
	}
}

func TestRepositoryWebIncludesRootAndMaterials(t *testing.T) {
	root := baseRepo(t)
	must(t, os.MkdirAll(filepath.Join(root, "one"), 0755))
	must(t, os.MkdirAll(filepath.Join(root, "two"), 0755))
	a := baseWrite(t, root, "one/a", "old a")
	b := baseWrite(t, root, "two/b", "old b")
	gone := baseWrite(t, root, "gone", "old gone")
	baseWrite(t, root, ".gitignore", "ignored\n")
	baseCommit(t, root)
	baseWrite(t, root, "one/a", "new a")
	baseWrite(t, root, "two/b", "staged b")
	baseGit(t, root, "add", "two/b")
	baseWrite(t, root, "two/b", "old b")
	must(t, os.Remove(gone))
	fresh := baseWrite(t, root, "fresh", "new")
	ignored := baseWrite(t, root, "ignored", "explicit")
	extra := baseWrite(t, t.TempDir(), "external", "extra")
	c, err := Open(t.TempDir())
	must(t, err)
	id := create(t, c, Submission{Path: a, Content: "new a", Original: orig("wrong")})
	cm := comment(t, c, id, a, "keep")
	_, err = c.PrepareWeb(Session{ID: "s", Cwd: filepath.Join(root, "one")}, id, []string{a, ignored, extra})
	must(t, err)
	v := rv(t, c, id)
	if v.Completeness == nil || !v.Completeness.Complete || len(v.Files) != 6 {
		t.Fatalf("%+v", v)
	}
	for _, path := range []string{a, b, gone, fresh, ignored, extra} {
		if _, err := c.File(id, path); err != nil {
			t.Fatal(path, err)
		}
	}
	if f := fv(t, c, id, a); f.Base != "old a" || f.Comments[0].ID != cm {
		t.Fatal(f)
	}
	if f := fv(t, c, id, b); f.RepositoryDiff != "changed" || f.Base != "old b" || f.Response == nil || *f.Response != "old b" {
		t.Fatal(f)
	}
	if f := fv(t, c, id, gone); f.Base != "old gone" || !f.ResponseDeleted {
		t.Fatal(f)
	}
	if f := fv(t, c, id, extra); f.RepositoryDiff != "" || f.BaseSource.Kind != "none" {
		t.Fatal(f)
	}
	if f := fv(t, c, id, fresh); f.RepositoryDiff != "added" {
		t.Fatal(f)
	}
	noAccepted(t, c, id)
}

func TestRepositoryDoneAndHookDiscoverCommandFiles(t *testing.T) {
	for _, mode := range []string{"done", "stop"} {
		t.Run(mode, func(t *testing.T) {
			c, id, root, a := repositoryReview(t)
			must(t, c.AcceptFile(id, a))
			p := baseWrite(t, root, "generated.md", "generated\n")
			if mode == "done" {
				_, err := c.Done("s", nil)
				must(t, err)
			} else {
				_, err := c.Stop("s", false)
				must(t, err)
			}
			f := fv(t, c, id, p)
			if f.Current != "generated\n" || f.HasBase || isViewAccepted(f) {
				t.Fatal(f)
			}
			if !rv(t, c, id).Completeness.Complete {
				t.Fatal(rv(t, c, id))
			}
			if fv(t, c, id, a).Base != "review\n" {
				t.Fatal("owner base lost")
			}
			noAccepted(t, c, id)
		})
	}
}
func isViewAccepted(f FileView) bool {
	return f.State == StateAccepted || f.State == StateDeletedAccepted
}

func TestRepositoryFreshAcceptanceAndDelivery(t *testing.T) {
	for _, stage := range []string{"accept", "deliver", "take-notices"} {
		t.Run(stage, func(t *testing.T) {
			c, id, root, a := repositoryReview(t)
			_, err := c.Stop("s", false)
			must(t, err)
			if stage != "accept" {
				must(t, c.AcceptFile(id, a))
				if !rv(t, c, id).Accepted {
					t.Fatal(rv(t, c, id))
				}
			}
			fresh := baseWrite(t, root, "late", "late")
			if stage == "accept" {
				must(t, c.AcceptFile(id, a))
			}
			if stage == "deliver" {
				_, ns, err := c.Deliver("s")
				must(t, err)
				for _, n := range ns {
					if n.Reason == NoticeAccepted {
						t.Fatal(ns)
					}
				}
			} else {
				noAccepted(t, c, id)
			}
			if f := fv(t, c, id, fresh); f.Current != "late" || isViewAccepted(f) {
				t.Fatal(f)
			}
			must(t, c.AcceptFile(id, fresh))
			_, ns, err := c.Deliver("s")
			must(t, err)
			count := 0
			for _, n := range ns {
				if n.Reason == NoticeAccepted {
					count++
				}
			}
			if count != 1 {
				t.Fatal(ns)
			}
			_, ns, err = c.Deliver("s")
			must(t, err)
			if len(ns) > 0 {
				t.Fatal(ns)
			}
		})
	}
}

func TestRepositoryConflictPreservesOtherReview(t *testing.T) {
	c, id, root, a := repositoryReview(t)
	other := baseWrite(t, root, "other", "text")
	id2 := create(t, c, Submission{Path: other, Content: "text"})
	cm := comment(t, c, id2, other, "keep elsewhere")
	must(t, c.AgentStart(id2))
	before, _ := json.Marshal(c.s.Files[other])
	reviewBefore, _ := json.Marshal(c.s.Reviews[id2])
	_, err := c.Done("s", nil)
	must(t, err)
	if !hasProblem(rv(t, c, id), "conflict", other) {
		t.Fatal(rv(t, c, id))
	}
	after, _ := json.Marshal(c.s.Files[other])
	reviewAfter, _ := json.Marshal(c.s.Reviews[id2])
	if string(before) != string(after) || string(reviewBefore) != string(reviewAfter) {
		t.Fatal("other review changed")
	}
	if fv(t, c, id2, other).Comments[0].ID != cm {
		t.Fatal("comment lost")
	}
	if f := fv(t, c, id, other); !f.ProblemOnly || f.Problems[0].ReviewID != id2 {
		t.Fatal(f)
	}
	if err = c.WriteCheck("s", other, true); err == nil {
		t.Fatal("write check moved a foreign file")
	}
	must(t, c.AcceptFile(id, a))
	noAccepted(t, c, id)
	c2, err := Open(c.Dir())
	must(t, err)
	if !hasProblem(rv(t, c2, id), "conflict", other) {
		t.Fatal("conflict not persisted")
	}
}

func TestRepositoryUnsupportedRecoveryAndOldBase(t *testing.T) {
	c, id, root, a := repositoryReview(t)
	bin := baseWrite(t, root, "binary", "a\x00b")
	f := c.s.Files[a]
	f.BaseSource = BaseSource{Kind: "unknown", Detail: "старый формат не записывал принятие при совпадении с базой"}
	oldBase := f.Base
	history := append([]BaseRecord(nil), f.BaseHistory...)
	_, err := c.Stop("s", false)
	must(t, err)
	v := rv(t, c, id)
	if !hasProblem(v, "unsupported", bin) || hasProblem(v, "unknown-base", a) {
		t.Fatal(v)
	}
	if f.Base != oldBase || !reflect.DeepEqual(f.BaseHistory, history) {
		t.Fatal("unknown base replaced")
	}
	if !fv(t, c, id, bin).ProblemOnly {
		t.Fatal("binary exposed as text")
	}
	must(t, c.AcceptFile(id, a))
	noAccepted(t, c, id)
	must(t, os.Remove(bin))
	must(t, c.RecheckRepository(id))
	if !rv(t, c, id).Accepted || !rv(t, c, id).Completeness.Complete {
		t.Fatal(rv(t, c, id))
	}
	c2, err := Open(c.Dir())
	must(t, err)
	if fv(t, c2, id, a).BaseSource.Kind != "owner" {
		t.Fatal("explicit acceptance lost")
	}
}

type failingRepository struct {
	vcs.Git
	stage string
}

func (p failingRepository) Detect(ctx context.Context, dir string) (*vcs.Workspace, error) {
	if p.stage == "detect" {
		return nil, errors.New("detect failed")
	}
	return p.Git.Detect(ctx, dir)
}
func (p failingRepository) State(ctx context.Context, w vcs.Workspace) (vcs.State, error) {
	if p.stage == "state" {
		return vcs.State{}, errors.New("state failed")
	}
	return p.Git.State(ctx, w)
}
func (p failingRepository) Original(ctx context.Context, w vcs.Workspace, rev, path string) (vcs.Content, error) {
	if p.stage == "original" {
		return vcs.Content{}, errors.New("original failed")
	}
	return p.Git.Original(ctx, w, rev, path)
}

func TestRepositoryProviderFailureIsNotExternal(t *testing.T) {
	for _, stage := range []string{"detect", "state", "original"} {
		t.Run(stage, func(t *testing.T) {
			c, id, root, a := repositoryReview(t)
			c.provider = failingRepository{stage: stage}
			base := fv(t, c, id, a).Base
			baseWrite(t, root, "a.md", "unread\n")
			_, err := c.Stop("s", false)
			must(t, err)
			v := rv(t, c, id)
			if v.Completeness.Complete || len(v.Completeness.Problems) == 0 {
				t.Fatal(v)
			}
			f := fv(t, c, id, a)
			if f.Current != "review\n" || f.Base != base || f.Response != nil {
				t.Fatal(f)
			}
			noAccepted(t, c, id)
			c2, err := Open(c.Dir())
			must(t, err)
			if rv(t, c2, id).Completeness.Complete {
				t.Fatal("failure lost")
			}
			c.provider = vcs.Git{}
			must(t, c.RecheckRepository(id))
			if !rv(t, c, id).Completeness.Complete || fv(t, c, id, a).Response == nil {
				t.Fatal(rv(t, c, id))
			}
		})
	}
}

func TestRepositoryPendingDefersBaseButKeepsResponseRules(t *testing.T) {
	c, id, root, a := repositoryReview(t)
	baseWrite(t, root, "a.md", "answer one\n")
	_, err := c.Done("s", nil)
	must(t, err)
	before := fv(t, c, id, a)
	if before.Response == nil {
		t.Fatal(before)
	}
	baseCommit(t, root)
	baseWrite(t, root, "a.md", "answer two\n")
	_, err = c.Done("s", nil)
	must(t, err)
	f := fv(t, c, id, a)
	if f.Base != before.Base || f.Current != before.Current || f.Response == nil || *f.Response != "answer two\n" {
		t.Fatal(f)
	}
	if !rv(t, c, id).Completeness.Complete {
		t.Fatal(rv(t, c, id))
	}
	must(t, c.AcceptResponse(id))
	must(t, c.SetBusy("s", false))
	must(t, c.RecheckRepository(id))
	if f = fv(t, c, id, a); f.Base != "answer one\n" || f.Current != "answer two\n" || isViewAccepted(f) {
		t.Fatal(f)
	}
}

func TestRepositoryEditorDoesNotChangeVersions(t *testing.T) {
	c, id, root, a := repositoryReview(t)
	must(t, c.SetBusy("s", false))
	must(t, c.SetEditing(id, true))
	before, _ := json.Marshal(c.s.Files[a])
	baseWrite(t, root, "a.md", "changed")
	_, err := c.Stop("s", false)
	must(t, err)
	after, _ := json.Marshal(c.s.Files[a])
	if string(before) != string(after) {
		t.Fatal("editor changed under hands")
	}
	if !hasProblem(rv(t, c, id), "deferred", "") {
		t.Fatal(rv(t, c, id))
	}
}

func TestRepositoryStagedThenMissingStillRequiresReview(t *testing.T) {
	c, id, root, a := repositoryReview(t)
	must(t, c.AcceptFile(id, a))
	_, err := c.Stop("s", false)
	must(t, err)
	_, _, err = c.Deliver("s")
	must(t, err)
	p := baseWrite(t, root, "only-index", "staged")
	baseGit(t, root, "add", "only-index")
	must(t, os.Remove(p))
	_, err = c.Done("s", nil)
	must(t, err)
	f := fv(t, c, id, p)
	if !f.CurrentDeleted || f.HasBase || isViewAccepted(f) {
		t.Fatal(f)
	}
	// Новые правки после уже доставленного полного принятия начинают новый переход.
	must(t, c.SetBusy("s", false))
	late := baseWrite(t, root, "even-later", "late")
	must(t, c.AcceptFile(id, p))
	if f = fv(t, c, id, late); f.Current != "late" {
		t.Fatal(f)
	}
	noAccepted(t, c, id)
}

type unstableRepository struct {
	vcs.Git
	n int
}

func (p *unstableRepository) State(ctx context.Context, w vcs.Workspace) (vcs.State, error) {
	s, e := p.Git.State(ctx, w)
	p.n++
	s.Token += string(rune('a' + p.n))
	return s, e
}
func TestRepositoryUnstableSnapshotDoesNotChangeVersions(t *testing.T) {
	c, id, root, a := repositoryReview(t)
	before, _ := json.Marshal(c.s.Files[a])
	baseWrite(t, root, "a.md", "unstable")
	c.provider = &unstableRepository{}
	_, err := c.Stop("s", false)
	must(t, err)
	after, _ := json.Marshal(c.s.Files[a])
	if string(before) != string(after) {
		t.Fatal("unstable snapshot applied")
	}
	if !hasProblem(rv(t, c, id), "changed", "") {
		t.Fatal(rv(t, c, id))
	}
	noAccepted(t, c, id)
}

func TestRepositoryFailureLeavesAvailableTextsReadable(t *testing.T) {
	c, id, root, a := repositoryReview(t)
	baseWrite(t, root, "binary", "\x00")
	p := baseWrite(t, root, "readable", "available")
	_, err := c.Stop("s", false)
	must(t, err)
	if fv(t, c, id, p).Current != "available" {
		t.Fatal("available material lost")
	}
	must(t, c.AcceptFile(id, p))
	must(t, c.AcceptFile(id, a))
	noAccepted(t, c, id)
}

func TestRepositoryRootAliasPreservesOwnershipAndBases(t *testing.T) {
	root := baseRepo(t)
	a := baseWrite(t, root, "a", "original")
	baseCommit(t, root)
	baseWrite(t, root, "a", "changed")
	link := filepath.Join(t.TempDir(), "repo-link")
	must(t, os.Symlink(root, link))
	aliasA := filepath.Join(link, "a")
	c, err := Open(t.TempDir())
	must(t, err)
	id := create(t, c, Submission{Path: aliasA, Content: "changed", Original: orig("wrong")})
	b := baseWrite(t, root, "b", "other")
	aliasB := filepath.Join(link, "b")
	other := create(t, c, Submission{Path: aliasB, Content: "other"})
	must(t, c.AcceptFile(other, aliasB))
	_, err = c.PrepareWeb(Session{ID: "s", Cwd: link}, id, []string{aliasA})
	must(t, err)
	if len(c.s.Files) != 2 {
		t.Fatalf("duplicate records: %+v", c.s.Files)
	}
	if f := fv(t, c, id, aliasA); f.Base != "original" || f.Current != "changed" {
		t.Fatal(f)
	}
	if hasProblem(rv(t, c, id), "conflict", aliasB) {
		t.Fatal("unchanged accepted file blocked review through an alias")
	}
	baseWrite(t, root, "b", "changed after acceptance")
	_, err = c.Done("s", nil)
	must(t, err)
	if !rv(t, c, id).Completeness.Complete || c.s.Files[aliasB].ReviewID != id ||
		fv(t, c, other, aliasB).MovedTo != id || c.s.Files[a] != nil || c.s.Files[b] != nil {
		t.Fatal("путь перенесён с дублированием или проверка не завершилась")
	}
}

func TestRepositoryNoRepeatedAcceptanceOnEmptyTurn(t *testing.T) {
	c, id, _, a := repositoryReview(t)
	_, err := c.Stop("s", false)
	must(t, err)
	must(t, c.AcceptFile(id, a))
	_, ns, err := c.Deliver("s")
	must(t, err)
	if len(ns) != 1 || ns[0].Reason != NoticeAccepted {
		t.Fatal(ns)
	}
	must(t, c.SetBusy("s", true))
	_, err = c.Stop("s", false)
	must(t, err)
	_, ns, err = c.Deliver("s")
	must(t, err)
	if len(ns) != 0 {
		t.Fatal("empty turn repeated full acceptance", ns)
	}
}

func TestRepositoryExplicitExternalConflictSurvivesRecheck(t *testing.T) {
	c, id, root, a := repositoryReview(t)
	external := baseWrite(t, t.TempDir(), "external", "foreign")
	create(t, c, Submission{Path: external, Content: "foreign"})
	_, err := c.PrepareWeb(Session{ID: "s", Cwd: root}, id, []string{external})
	if err == nil {
		t.Fatal("foreign explicit material allowed")
	}
	_, err = c.Stop("s", false)
	must(t, err)
	must(t, c.AcceptFile(id, a))
	must(t, c.RecheckRepository(id))
	if !hasProblem(rv(t, c, id), "conflict", external) {
		t.Fatal("explicit material forgotten")
	}
	noAccepted(t, c, id)
}

type delayedRepositoryProvider struct {
	vcs.Provider
	started chan struct{}
	resume  chan struct{}
}

func (p delayedRepositoryProvider) Detect(ctx context.Context, dir string) (*vcs.Workspace, error) {
	select {
	case <-p.started:
	default:
		close(p.started)
	}
	select {
	case <-p.resume:
		return p.Provider.Detect(ctx, dir)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestBackgroundStopPublishesAfterRepositoryCheck(t *testing.T) {
	c, id, root, path := repositoryReview(t)
	added := baseWrite(t, root, "from-stop.md", "found in the background\n")
	provider := delayedRepositoryProvider{c.provider, make(chan struct{}), make(chan struct{})}
	c.provider = provider
	resume := sync.OnceFunc(func() { close(provider.resume) })
	defer resume()

	returned := make(chan error, 1)
	go func() {
		_, err := c.StopInBackground("s", false)
		returned <- err
	}()
	select {
	case err := <-returned:
		must(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Stop waited for the repository provider")
	}
	select {
	case <-provider.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the background repository check did not start")
	}
	if !c.Busy("s") {
		t.Fatal("the review was released while collection was running")
	}
	resume()
	waitForBackgroundStop(t, c, id)
	if c.Busy("s") {
		t.Fatal("the session stayed busy after publication")
	}
	if _, err := c.File(id, added); err != nil {
		t.Fatal("missing repository change:", err)
	}
	cm := comment(t, c, id, path, "please answer before stopping")
	must(t, c.Send(id, false))
	_, _, err := c.Deliver("s")
	must(t, err)
	res, err := c.StopInBackground("s", false)
	must(t, err)
	if len(res.Unanswered) != 1 || res.Unanswered[0].ID != cm {
		t.Fatalf("Stop did not block on the delivered comment: %+v", res)
	}
}

func TestBackgroundStopCannotFinishNewTurn(t *testing.T) {
	c, id, root, _ := repositoryReview(t)
	added := baseWrite(t, root, "next-turn.md", "still being edited\n")
	provider := delayedRepositoryProvider{c.provider, make(chan struct{}), make(chan struct{})}
	c.provider = provider
	defer close(provider.resume)
	_, err := c.StopInBackground("s", false)
	must(t, err)
	select {
	case <-provider.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the background repository check did not start")
	}
	must(t, c.SetBusy("s", true))
	waitForBackgroundStop(t, c, id)
	if _, err := c.File(id, added); err == nil || !c.Busy("s") {
		t.Fatal("the previous Stop published changes or released the new turn")
	}
}

func waitForBackgroundStop(t *testing.T, c *Core, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		done := c.s.Reviews[id].backgroundStop == nil && !c.repositoryChecks[id]
		c.mu.Unlock()
		if done {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the background Stop did not finish")
}

func TestRepositoryCheckAllowsReadingAndSavingComments(t *testing.T) {
	c, id, _, path := repositoryReview(t)
	_, err := c.Stop("s", false)
	must(t, err)
	provider := delayedRepositoryProvider{c.provider, make(chan struct{}), make(chan struct{})}
	c.provider = provider
	defer func() {
		if provider.resume != nil {
			close(provider.resume)
		}
	}()
	check := make(chan error, 1)
	go func() { check <- c.RecheckRepository(id) }()
	select {
	case <-provider.started:
	case err := <-check:
		t.Fatalf("check did not start: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("check did not start")
	}
	result := make(chan error, 1)
	go func() {
		if _, err := c.File(id, path); err != nil {
			result <- err
			return
		}
		_, err := c.AddComment(id, path, nil, "сохранён во время проверки", nil, false)
		result <- err
	}()
	select {
	case err := <-result:
		must(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("repository check blocked reading or commenting")
	}
	close(provider.resume)
	provider.resume = nil
	must(t, <-check)
	if comments := fv(t, c, id, path).Comments; len(comments) != 1 {
		t.Fatalf("comments: %+v", comments)
	}
}

func TestRecoverRepositoryKeepsAcceptedBaseAndComments(t *testing.T) {
	c, source, root, path := repositoryReview(t)
	_, err := c.Stop("s", false)
	must(t, err)
	cm := comment(t, c, source, path, "сохраняется после переноса")
	must(t, c.AcceptFile(source, path))
	baseWrite(t, root, "a.md", "changed after acceptance")
	next := baseWrite(t, root, "tasks.md", "# Задачи")
	target := create(t, c, Submission{Path: next, Content: "# Задачи"})
	_, err = c.PrepareWeb(Session{ID: "next", Cwd: root}, target, []string{next})
	must(t, err)
	_, err = c.Stop("next", false)
	must(t, err)
	must(t, c.RecheckRepository(target))
	file := fv(t, c, target, path)
	if file.Base != "review\n" || len(file.Comments) != 1 || file.Comments[0].ID != cm {
		t.Fatalf("file after recovery: %+v", file)
	}
	if v := rv(t, c, target); !v.Completeness.Complete {
		t.Fatalf("recovery incomplete: %+v", v.Completeness)
	}
	if moved := fv(t, c, source, path); moved.MovedTo != target {
		t.Fatalf("moved: %+v", moved)
	}
	if matches := c.ReviewsForWorkspace(root); len(matches) != 1 || matches[0].ID != target {
		t.Fatalf("matches: %+v", matches)
	}
}

func TestAcceptedFileCanBeEditedFromAnotherReview(t *testing.T) {
	c, source, root, path := repositoryReview(t)
	_, err := c.Stop("s", false)
	must(t, err)
	cm := comment(t, c, source, path, "комментарий сохраняется")
	must(t, c.AcceptFile(source, path))

	next := baseWrite(t, root, "tasks.md", "# Задачи")
	target := create(t, c, Submission{Path: next, Content: "# Задачи"})
	must(t, c.SetBusy("s", true))
	_, err = c.PrepareWeb(Session{ID: "next", Cwd: root}, target, []string{next})
	must(t, err)
	if !hasProblem(rv(t, c, target), "conflict", path) {
		t.Fatal("параллельная работа не видна")
	}
	if err := c.WriteCheck("next", path, true); err == nil || !strings.Contains(err.Error(), "агент другого ревью работает") {
		t.Fatalf("concurrent agent was not protected: %v", err)
	}
	must(t, c.SetBusy("s", false))
	must(t, c.WriteCheck("next", path, true))
	if moved := fv(t, c, source, path); moved.MovedTo != target {
		t.Fatalf("file did not move: %+v", moved)
	}

	baseWrite(t, root, "a.md", "new version")
	_, err = c.Stop("next", false)
	must(t, err)
	file := fv(t, c, target, path)
	if file.Base != "review\n" || file.Current != "new version" || file.State == StateAccepted ||
		len(file.Comments) != 1 || file.Comments[0].ID != cm {
		t.Fatalf("new version or history lost: %+v", file)
	}
}

func TestRepositoryDiffAfterRollback(t *testing.T) {
	for _, accept := range []bool{true, false} {
		t.Run(map[bool]string{true: "просмотр", false: "отклонение"}[accept], func(t *testing.T) {
			c, id, root, path := repositoryReview(t)
			if f := fv(t, c, id, path); f.RepositoryDiff != "changed" {
				t.Fatal(f)
			}
			cm := comment(t, c, id, path, "Вернуть исходный текст")
			must(t, c.Send(id, false))
			c.Deliver("s")
			baseWrite(t, root, "a.md", "committed\n")
			_, err := c.Done("s", []Answer{{CommentID: cm, Text: "Вернул"}})
			must(t, err)
			if f := fv(t, c, id, path); f.RepositoryDiff != "unchanged" || !f.Ripple ||
				f.Response == nil || *f.Response != "committed\n" {
				t.Fatal(f)
			}
			if !accept {
				must(t, c.RejectResponse(id))
				if f := fv(t, c, id, path); f.RepositoryDiff == "unchanged" || f.Current != "review\n" {
					t.Fatal(f)
				}
				return
			}
			must(t, c.AcceptResponse(id))
			if f := fv(t, c, id, path); f.RepositoryDiff != "unchanged" || len(f.Comments) != 1 {
				t.Fatal(f)
			}
			_, err = c.Stop("s", false)
			must(t, err)
			baseWrite(t, root, "a.md", "changed again\n")
			must(t, c.RecheckRepository(id))
			if f := fv(t, c, id, path); f.RepositoryDiff != "changed" {
				t.Fatal(f)
			}
		})
	}
}
