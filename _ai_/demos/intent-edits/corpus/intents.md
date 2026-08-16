# Intent edits: per-commit notes

Twenty commits, expressed in the intent grammar from [`DESIGN.md`](../../../../DESIGN.md#intent-edits), with notes on what a resolver would need to know and where it would fail. Bucket counts and intent lines are judgment calls made by hand; see `README.md` for the caveat and the aggregate numbers.

## gh-repo-dashboard cd69133: feat(stash): render the full patch through the configured diff viewer

```
add field Config.Diff.External string toml=diff.external doc="external diff command overriding git's diff.external"
add fn (g *GitOperations) ExternalDiffCommand(ctx, repoPath) string in internal/vcs near StashDiff like=StashDiff
add fn (*GitOperations) StashDiffExternal(ctx, repoPath, index, width int, command string) (string, error) in internal/vcs like=StashDiff env=COLUMNS,CLICOLOR_FORCE,DFT_COLOR,DFT_DISPLAY,DFT_WIDTH
change fn loadStashDiffCmd(repoPath, index, width) to branch on ExternalDiffCommand before falling back to StashDiff
add fn stashBodyLine(line string, width int) string in internal/app near stashDetailLines ansi-aware=yes
```

The resolver mirrors `internal/vcs`'s existing `GitOperations` method shape (receiver, error wrapping, `runCommandRaw`, test scaffolding via the repo's `stubCommands` helper) and registers `DiffConfig` into `Config` the same mechanical way `CacheToDisk` was added. It fails on which env vars difftastic/delta actually read (`COLUMNS`, `DFT_WIDTH`, `CLICOLOR_FORCE`, `DFT_COLOR`, `DFT_DISPLAY`), tool-specific knowledge with no sibling to copy, and on the ANSI-escape detection heuristic in `stashBodyLine` (skip restyling once a line already carries an escape code).

## gh-repo-dashboard 44588fd: fix(cli): persist the per-branch PR and default-branch CI like the PR list

```
like PRCache: add DefaultBranchCICache in internal/cache/ttl.go ttl=workflowTTL
like PRCacheKey: add fn DefaultBranchCICacheKey(repoPath, remoteID, sha string) string in internal/github/workflow.go
like CachedPRForBranch: add fn CachedDefaultBranchCI(ctx, repoPath, remoteID string) (*models.DefaultBranchCI, bool) in internal/github/workflow.go
change signature GetDefaultBranchCI(ctx, repoPath, remoteID string) and thread remoteID through cli.lookupCI, cli.githubClient.defaultCI, export_test.NewGitHubClientWithCI
add test for CachedDefaultBranchCI like=TestGitHubValuesSurviveAColdMemoryCache in internal/github/cache_test.go
```

This is close to the textbook `like Foo: add Bar` case, but the sibling here is a cluster, not one symbol: `DefaultBranchCICache`, its key builder, and its cached-read wrapper each have a literal same-file sibling to diff-copy and rename, and `GetDefaultBranchCI`'s cache-check-then-persist body mirrors `GetPRForBranch`'s pattern a few lines above it. Threading `remoteID` through the call chain is mechanical propagation off one signature edit. The one place needing real judgment is the cache-key design: embedding the commit SHA so a moved default branch becomes a new entry rather than a stale hit is a heuristic the resolver would need pre-encoded, not something it reasons out fresh.

## gh-repo-dashboard 5a22239: feat(script): fail on rejected commands and list :commands headlessly

```
add field Model.headless bool in internal/app/app.go near commandMode doc="script mode answers in the status line instead of opening a modal"
add field StatusMsg.IsError bool in internal/app/messages.go doc="marks a rejected command; headless mode turns it into a nonzero exit"
add fn statusErrCmd(message string) tea.Cmd in internal/app/command.go like=statusCmd but sets IsError=true
change signature runScriptLine(m Model, line string, w io.Writer) (Model, lineOutcome), replacing the bool quit return; add type lineOutcome{quit, failed bool}
add fn (Registry) Listing() string in internal/app/command.go near Candidates
change condition: RunScript continues past a failed line instead of returning early, counts failures, returns errScriptFailed when any occurred
```

The mass `statusCmd` → `statusErrCmd` reclassification (8 call sites) follows a heuristic a code-graph layer could apply ("a status message reporting a rejected input becomes an error"), but each call site still needs a judgment call about whether that message counts as a rejection. The `lineOutcome` struct and its threading through `runScriptLine`/`RunScript` is mechanical scaffolding; the actual failure-detection logic is a hole. `script_test.go`'s rewrite from four standalone tests into two table-driven tests has no resolver basis; it would only know how to append cases to the existing layout, not restructure it.

## gh-repo-dashboard 5249714: fix(cli): reject a scan path that is not an existing directory

```
add fn checkScanPaths(source string, paths []string) error in cmd/gh-repo-dashboard/main.go near resolveScanPaths condition="os.Stat succeeds and info.IsDir()" message="%s %s: %w"
add var errNotDirectory = errors.New("not a directory") in cmd/gh-repo-dashboard/main.go near errEmptyRoster
wire checkScanPaths into resolveScanPaths for both the positional-args branch and the cfg.ScanPaths branch
add test for checkScanPaths like=TestFindGitRoot table-driven
```

No true sibling existed for "validate a scan path," so the resolver falls back to the repo's generic validating-helper idiom (`os.Stat`, wrap with `fmt.Errorf("%s %s: %w", ...)`) instead of mirroring a named function. Wiring the two call sites is mechanical once `checkScanPaths` exists, and the test scaffold mirrors nearby tests in the same file. The hole is the condition itself (stat-fails vs. not-a-directory) and the message template; the judgment call, documented in the added comment, is validating only the explicitly named roots and leaving the cwd/enclosing-repo fallback unchecked "by construction."

## gh-repo-dashboard 884fbcf: fix(list): stop the fleet map from discarding the expanded region's cache

```
fix: m.prMap is shared by the fleet PR map view and the expanded region's cache; stop clearing it in handlePRMapKey's Back branch, openPRMapRepo, and openPRMap's setup
add fn (m *Model) prMapPending() int in internal/app/prmap.go near fetchPending, replacing len(m.prMap) comparisons as the "still loading" signal
wire m.startFetch(path, fetchExpand) into openPRMap's per-repo loop so prMapPending has a tracker entry to count
add test for prMapPending like=TestPRMapEnterOpensTheRepoBehindTheRow
```

Grammar extension needed: a `fix: <diagnosis>` verb. This is a genuine bug fix, not new plumbing, so there is no sibling to mirror; the fix is the diagnosis itself (two features shared one map, and the fleet map's enter/exit code was clearing a cache it did not own). A resolver could mechanically delete the three `m.prMap = nil` lines and scaffold `prMapPending`'s loop shape once told what to count, but recognizing that `len(m.prMap)` was the wrong "still loading" signal required tracing both call sites through the fetch tracker, squarely a judgment call.

## gh-repo-dashboard a5bbb28: refactor(list): collapse the breakpoint enum now that wide renders like standard

```
like breakpointFor: collapse breakpoint(enum) -> compactLayout(width int) bool in internal/app near standardMinWidth threshold=100 drop=wideMinWidth,widePanelMinHeight
update test TestBreakpointForSize -> TestCompactLayoutFollowsWidthAlone like=compactLayout test=yes
update doc docs/design/layout-and-density.md section "Breakpoints (M14)" reason="wide layout removed, panel gone"
```

Grammar extension needed: `collapse <old> -> <new>` with `drop=<symbols>` to name constants deleted alongside the collapse. The resolver mirrors the existing `isCompact`/`breakpointFor` call sites mechanically (rename, drop the height parameter, retype the const block) and regenerates the test table from the old one, covering the bulk of the diff. It cannot invent the doc paragraph explaining why the third breakpoint disappeared; that is a design-rationale hole best flagged for a human/model pass rather than auto-filled.

## gh-repo-dashboard 7be9b61: feat(vcs): serve branch lists and commit logs while the stamp holds

```
like cachedBranchList: add fn cachedCommitLog(repoPath string, count int, read func() ([]models.CommitInfo, error)) ([]models.CommitInfo, error) in internal/vcs near stamped wraps=cache.CommitCache,cache.CommitCacheKey
add fn stamped[T any](c *cache.TTLCache[T], key, repoPath string, read func() (T, error)) (T, error) in internal/vcs/cached.go generic=true
wire GitOperations.GetBranchList,GetCommitLog and JJOperations.GetBranchList,GetCommitLog through cachedBranchList/cachedCommitLog, renaming the old bodies to branchList/commitLog
doc DESIGN.md bullet "Cache invalidation" explain=vcs.Stamp,Fresh,TTL-ceiling-for-remote-values
```

`cachedBranchList`/`cachedCommitLog` mirror each other exactly, a textbook `like` case: generate the second from the first by substitution. Wiring the four call sites (Git/JJ × Branch/Commit) is pure renaming plus a one-line delegating wrapper, deterministic from the existing `Stamp`/`cache.Fresh` API. The DESIGN.md paragraph explaining why local values get proven-fresh treatment while remote values only get a TTL ceiling is architectural reasoning, not derivable from the diff alone.

## gh-repo-dashboard 0c5f099: feat(vcs): derive a host-qualified upstream identity for each remote

```
add field RepoSummary.RemoteID string in internal/models/repo.go doc="host/owner/repo lowercased cache identity of the remote"
add fn RemoteIdentity(remoteURL string) string in internal/vcs/identity.go near CheckoutIdentity handles=ssh,https,http,scp-style,port,case-fold
add fn RemoteIdentityFor(ctx context.Context, repoPath string) string in internal/vcs near RemoteIdentity wraps=GetOperations(repoPath).GetRemoteURL
wire GitOperations.GetRepoSummary,JJOperations.GetRepoSummary to set summary.RemoteID = RemoteIdentity(remoteURL)
add test TestRemoteIdentity like=TestCheckoutIdentity test=yes cases=11
```

The field addition and both call-site wirings are deterministic or convention-level (`summary.RemoteID = RemoteIdentity(remoteURL)` mirrors the adjacent `summary.RemoteRepo = ExtractRepoPath(remoteURL)` line exactly). `RemoteIdentity` itself is the hole: parsing SSH/HTTPS/SCP-style URLs, stripping credentials and ports, and disambiguating a port from a path is genuine algorithmic logic no resolver reliably nails without the 11 test cases already encoded.

## gh-repo-dashboard d069317: feat(panels): name the failing checks in the PR detail pane

```
add fn prCheckLines(checks []models.CheckDetail, width int) []string in internal/app/view_panels.go near prDetailLines uses=checkDisplayName,StatusDisplay,styles.HeaderStyle
add fn unsettledChecks(checks []models.CheckDetail) []models.CheckDetail returns=failing-then-running order
add fn settledTally(checks []models.CheckDetail) []string group-by=StatusDisplay() count=true
add fn checkRank(status string) int enum=checkRankFailing,checkRankRunning,checkRankSettled
change fn handlePRDetailLoaded condition msg.PRNumber != m.selectedPR.Number -> !m.showingPR(msg.PRNumber) reason="focused view never sets selectedPR"
add fn showingPR(number int) bool like=selectedPanelPR
add test TestPRDetailNamesTheFailingChecks,TestPRDetailLandsWhenOnlyThePanelCursorSelectsIt test=yes
```

`checkRank`'s dispatch mirrors the existing `checkStatusStyle` switch almost verbatim (`like checkStatusStyle: add checkRank`), and the width/style plumbing follows the layout convention already used elsewhere in `view_panels.go`. The real hole is the display policy: which checks get their own row versus collapse into a tally, the fail-then-running ordering, the row cap with a "N more" line, and the `showingPR` bug fix (the focused view tracks selection via panel cursor, not `selectedPR`), a UI/state-model judgment call with no grammar line that derives it.

## gh-repo-dashboard afdfbbe: feat(panels): let a stash verb swap the diffstat for its full diff

```
like StashDiffstat: add fn StashDiff(ctx, repoPath string, index int) (string, error) in internal/vcs/git.go near StashDiffstat cmd="stash show --patch --no-color"
add field Model.stashDiff map[int]string, Model.stashFullDiff bool in internal/app/app.go near stashDiffstat
add command o -> toggleStashDiff in panelActionsFor(panelStashes) like=startApplyStash name="toggle full diff"
like loadStashDiffstatCmd: add fn loadStashDiffCmd(repoPath string, index int) tea.Cmd wraps=vcs.GitOperations.StashDiff
like StashDiffstatLoadedMsg: add type StashDiffLoadedMsg{Path string; Index int; Diff string}
change fn stashDetailLines to branch on m.stashFullDiff between m.stashDiffstat/m.stashDiff maps, label="diffstat"|"diff"
add fn stashBodyLines(body string) []string cap=stashDiffMaxLines=500 suffix="... N more lines"
add test TestStashFullDiffVerbSwapsTheDetailPane test=yes
```

Close to a pure `like` commit: `StashDiff` mirrors `StashDiffstat` with one flag changed, `loadStashDiffCmd` mirrors `loadStashDiffstatCmd`, `StashDiffLoadedMsg` mirrors `StashDiffstatLoadedMsg`, and the `o` keybinding mirrors the existing stash verbs already registered in `panelActionsFor`. A resolver with a code-graph store could generate nearly all of it by substitution. The toggle/caching state machine is a small hole, and the 500-line cap with its "N more lines" message is a minor judgment call with no sibling precedent in this diff.

## gh-repo-dashboard a01befc: fix(panels): carry j/k across panel boundaries so a one-row panel can't swallow them

```
change fn moveDetailCursor(panels []panelContent, delta int) in internal/app near moveDetailCursor sig=old->new
add fn crossPanel(panels []panelContent, delta int) in internal/app near moveDetailCursor
add test for moveDetailCursor like=TestDetailCursorMovement name=TestDetailCursorCrossesPanelsWithNothingToMoveThrough
```

The resolver can thread the new `panels []panelContent` parameter through the two call sites in `handlePanelMoveKey` (mirrors an existing pattern where `cyclePanel` already takes `panels`) and stub `crossPanel`/`panelRowCount` from the signature and the renamed `selectPanelRow`. It cannot invent the boundary-carry logic itself (which panel to land on, clamp vs. cross, cursor at 0 vs. last row) or the table-driven test cases naming specific panels and transitions, a UX decision (carry through, don't wrap) the model has to be told about explicitly.

## gh-repo-dashboard c61e6a4: fix(list): keep the notes key on offer and say when the terminal is too short

```
add fn toggleNotesPreview() in internal/app near notesPreviewCmd like=handleKey.NotesPreview-case body="open/close with too-short guard, status message on fail"
change fn footerHints() -> footerHints(notesOpen bool) in internal/app near footerHints reprioritize v-hint when open
```

The resolver mirrors the existing extract-a-helper pattern already used elsewhere in `update.go` and the existing `footerHint` struct/priority table, so struct literals and the call-site swap are deterministic. The hole is the guard condition (call `notesPreviewHeight`, check ==0, revert and set a message) and the specific new priority numbers chosen to reorder the footer, UX judgment a small model would need explicit values for.

## gh-repo-dashboard df88120: feat(list): close the notes preview with a divider that captions it

```
add fn qualifiedRepoName(path string) in internal/app near notesPreviewLines body="join parent dir and base name"
add fn notesDivider(repo, detail string, width int) in internal/app like=notesFileRule(renamed from notesPreviewRule)
add fn padTop(lines []string, height int) in internal/app near fitBlock
rename+replace-callers overviewEmpty -> emDash across overviewPeers/overviewCount/overviewPRs/overviewNotes (4 call sites)
add test for notesPreviewLines like=TestNotesPanel_ShowsTheNoteWithoutFocusingIt name=TestNotesPreview_CaptionsTheRegionAtItsDivider
```

Grammar extension needed: `rename+replace-callers` as a batch op across a fixed call-site set. The `overviewEmpty` to `emDash` rename and its four call-site substitutions, plus golden/fixture regeneration, is exactly what a code-graph-aware resolver is built for: find all callers of a package-level const, swap the literal, regenerate goldens. `notesFileRule` already exists as the sibling `notesDivider` should mirror. It cannot derive the UX call to caption at the bottom instead of the top, or the `padTop` vs. `fitBlock` choice for short-note framing, judgment calls stated in the commit body, not derivable from any sibling.

## gh-repo-dashboard c497fd6: feat(list): dismiss the notes panel, then the search, then the filters on esc

```
add fn filtersActive() bool in internal/app near ActiveFilterModes body="true if predicate set or any enabled filter != All"
add fn dismissListLayer() in internal/app near handleBackKey case ViewModeRepoList body="close notes preview, else clear search, else clear filters, priority order"
add test for dismissListLayer like=TestNotesPreview_CaptionsTheRegionAtItsDivider name=TestEscape_DismissesOneListLayerAtATime
```

The switch-case wiring into `handleBackKey` is deterministic, mirroring the existing case pattern immediately above it. `filtersActive` is mostly convention, readable off the `Model` struct fields once told which ones count. `dismissListLayer`'s three-branch priority order (notes, then search, then filters) and the choice to reset cursor/call `updateFilteredPaths` on two of three branches is a genuine design decision a hole-filling model would need spelled out, or would guess wrong, closer to escalation (layer 4) than a single-hole fill.

## calcipy f55353fc: feat(vale): add vale for prose linting

```
add fn prose(ctx, target=".", no_sync=False) in calcipy/tasks/lint.py near watch like=watch body="check_installed(vale), vale sync unless no_sync, run vale target"
add field VALE_MESSAGE in calcipy/tasks/executable_utils.py like=PYRIGHT_MESSAGE
add hook prose -> calcipy-lint lint.prose in .pre-commit-config.yaml, .pre-commit-hooks.yaml like=tags stage=manual
add test for prose like=test_lint[pre_commit] cases
add doc docs/docs/adr/0008-adopt-vale-ai-tells-for-prose-linting.md, docs/docs/adr-research/ai-slop-detection.md
```

Grammar extensions needed: `add doc <path>` as its own verb producing full prose content, and `add hook` as a route-like registration verb for pre-commit's two YAML manifests. The `prose` task is a near-exact structural mirror of the existing `check_installed`-gated tasks, and the YAML hook entries mirror `tags`'s entry field-for-field, so a resolver plus one sibling lookup gets nearly all of the code, config, and docs/README.md's autogenerated subcommand line right. The huge judgment bucket is two long-form Markdown documents (142 + 117 lines): the ADR's decision rationale and the research doc's tool comparison and prose. This commit's "intent" is really a documentation-authoring task wearing a code-change diffstat, and the grammar has no verb for that today.

## calcipy ef745676: feat: support jj repos

```
bump dep corallium>=2.3.0rc1
like test_collect_code_tags_pure_jj_from_subdirectory: add test_collect_code_tags_ignore_repo_root_flag_jj
like test_collect_code_tags_pure_jj_from_subdirectory: add test_collect_code_tags_copier_answers_at_repo_root_jj
```

Grammar extension needed: `bump dep <name><specifier>`. The actual jj support lives in the `corallium` dependency, not in calcipy; this commit is almost entirely mechanical fallout of that bump (re-pointing two imports, regenerating `uv.lock`/CHANGELOG/coverage tables). The two new tests are near-exact clones of the adjacent sibling test, a textbook `like` mirror. The one place the resolver needs a heuristic rather than pure determinism is discovering that `find_repo_root` moved packages in the new release, which requires reading the dependency's public API, not just local code-graph matching.

## calcipy b63beb01: feat: support djot in markdown writer

```
rename module calcipy/md_writer -> calcipy/markup_writer, calcipy/markdown_table.py -> calcipy/markup_table.py
rename fn write_template_formatted_md_sections -> write_template_formatted_dj_sections in calcipy/markup_writer/_writer.py params paths_md=paths_dj
change comment delimiter syntax from `<!-- {cts} ... -->` to generic `[cts]`/`{% [cts] %}` markup markers to support djot in addition to markdown
```

Grammar extensions needed: `rename module A -> B` and `change delimiter syntax from X to Y`; neither is a single-symbol edit. The package/file renames and caller updates are pure mechanical propagation a code-graph pass could drive. The genuine judgment call is the new comment-delimiter regex itself, switching from an HTML-only pattern to a generic one that also matches djot/Jinja-style comments, a cross-format design decision with no sibling to mirror, since it is the first place multi-format comment markers are introduced in this repo.

## calcipy a72a9cd6: feat: implement new CLI_OUTPUT markdown tool

```
like _handle_coverage: add fn _handle_cli_output(line, _path_file) -> List[str] in calcipy/markup_writer/_writer.py test=yes
add field lookup['CLI_OUTPUT='] = _handle_cli_output in write_template_formatted_dj_sections handler_lookup
add const _CLI_ALLOWED_PREFIXES = ('./run', 'uv ', 'python -m ', 'python3 -m ') doc="command allowlist for CLI_OUTPUT execution"
```

The cleanest `like` case in the corpus: `_handle_cli_output` mirrors `_handle_coverage`/`_handle_source_file` almost exactly in shape, and the three new tests mirror existing handler tests. The 80-line docs/README.md addition is captured `--help` output the tool itself would regenerate on doc build, so it is generated rather than authored. The genuine hole/judgment is the security-relevant allowlist of command prefixes and the decision to shell out via raw `subprocess.run` instead of the repo's existing `capture_shell` wrapper, a deviation from convention a resolver should flag rather than silently apply.

## calcipy 89fac604: fix: use git root to create exactly one code tag summary

```
add field collect_code_tags.ignore_git_root bool default=false doc="Ignore git root check and use current directory as base"
add condition: collect_code_tags resolves git root via `git rev-parse --show-toplevel`, warns and rebases pth_base_dir to git_root when cwd != git_root and ignore_git_root is false, else raises RuntimeError if not in a git repo
like test_collect_code_tags_jj_repo: add test_collect_code_tags_from_subdirectory_uses_git_root, test_collect_code_tags_ignore_git_root_flag, test_collect_code_tags_not_in_git_repo
```

The kwarg addition and the keyword-only signature change are pure wiring. The three tests are convention-mirrors of the file's existing git-repo-setup pattern. The core judgment call is the behavior itself, warn-and-rebase vs. hard error, and raising when git is entirely absent. Worth flagging: the diff reimplements ad hoc git-root detection with a raw `capture_shell('git rev-parse --show-toplevel')` call that the very next tracked commit (`ef745676`) replaces by centralizing into `corallium.vcs.find_repo_root`. A resolver with a code-graph store should have caught that a near-identical root-finding helper already existed in `calcipy/invoke_helpers.py` and reused it instead of duplicating logic. The diff also carries a real authoring bug: `import os` and `import pytest` are each added twice in `tests/tasks/test_tags.py`, which a deterministic import/format layer would catch and dedupe.

## calcipy e479edce: test: add tests for jj repos

```
like _init_git_repo/_commit_files: add fn _jj_available, _init_jj_repo, _jj_track_files, fixture skip_if_no_jj in tests/tasks/test_tags.py
like test_collect_code_tags_jj_repo: add test test_collect_code_tags_pure_jj_repo, test_collect_code_tags_pure_jj_from_subdirectory, test_find_repo_root_pure_jj
```

Almost entirely a `like` commit: the jj helper fixtures are one-to-one substitutions of the file's existing git-init helpers, and the three new tests mirror the shape of the existing git-based sibling tests exactly, swapping fixture calls and adding the `skip_if_no_jj` guard. The one place a resolver needs a real hole is knowing the correct jj CLI syntax itself (`jj git init --no-colocate`, `jj config set --repo user.email ...`), domain knowledge about a specific tool's command surface, not inferable from the surrounding Python.
