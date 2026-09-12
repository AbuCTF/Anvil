<script lang="ts">
  import Icon from "@iconify/svelte";
  import { onMount } from "svelte";
  import { API_BASE } from "$lib/config";
  import { formatDur } from "$lib/chart/time";
  import { formatLocalDateTime, instantTitle } from "$lib/time";
  import LineChart from "$lib/components/LineChart.svelte";
  import Sparkline from "$lib/components/Sparkline.svelte";
  import PageHeader from "$lib/components/PageHeader.svelte";
  import Card from "$lib/components/Card.svelte";
  import EmptyState from "$lib/components/EmptyState.svelte";
  import OpticalIcon from "$lib/components/OpticalIcon.svelte";
  import ScoreboardMatrix from "$lib/components/ScoreboardMatrix.svelte";
  import { teamColor, rankAccent } from "$lib/rank";
  import { downloadRankCard } from "$lib/share";
  import type { Series } from "$lib/chart/path";

  interface Entry {
    rank: number;
    user_id: string;
    username: string;
    display_name?: string;
    total_score: number;
    challenges_solved: number;
    flags_solved: number;
    last_solve_at?: string;
    delta: number;
    spark?: number[];
  }

  interface HistorySeries {
    id: string;
    label: string;
    points: { x: number; y: number }[];
  }

  interface MatrixChallenge {
    slug: string;
    name: string;
    category: string;
    category_color: string;
    points: number;
  }

  interface MatrixRow {
    rank: number;
    user_id: string;
    username: string;
    name: string;
    total: number;
    delta: number;
    cells: { s: number; b: number }[];
  }

  const SCORE_POLL_MS = 5000;
  const HISTORY_POLL_MS = 30000;
  const MATRIX_POLL_MS = 15000;
  const SCORE_PAGE_SIZE = 100;

  let entries: Entry[] = [];
  let totalUsers = 0;
  let matchingUsers = 0;
  let raceSeries: Series[] = [];
  let matrixChallenges: MatrixChallenge[] = [];
  let matrixRows: MatrixRow[] = [];
  let matrixTotalUsers = 0;
  let loading = true;
  let matrixLoading = true;
  let error = "";
  let matrixError = "";
  let scoreStale = false;
  let historyStale = false;
  let historyError = "";
  let scoresLoaded = false;
  let scoreInFlight = false;
  let historyInFlight = false;
  let matrixInFlight = false;
  let scoreTimer: ReturnType<typeof setTimeout>;
  let historyTimer: ReturnType<typeof setTimeout>;
  let matrixTimer: ReturnType<typeof setTimeout>;
  let requestController: AbortController;
  let scoreETag = "";
  let scoreETagKey = "";
  let historyETag = "";
  let matrixETag = "";
  let scoreReloadPending = false;
  let searchDebounce: ReturnType<typeof setTimeout>;

  let search = "";
  let sortKey: "rank" | "name" = "rank";
  let view: "matrix" | "standings" = "matrix";
  let category = "";
  let scorePage = 1;

  const jitter = (interval: number) => interval * (0.85 + Math.random() * 0.3);
  function authHeaders(): Record<string, string> {
    if (typeof localStorage === "undefined") return {};
    const token = localStorage.getItem("accessToken");
    return token ? { Authorization: `Bearer ${token}` } : {};
  }

  async function loadScores() {
    if (scoreInFlight) {
      scoreReloadPending = true;
      return;
    }
    if (document.hidden) return;
    scoreInFlight = true;
    const requestedPage = view === "standings" ? scorePage : 1;
    const requestedQuery = view === "standings" ? search.trim() : "";
    const requestedSort = view === "standings" ? sortKey : "rank";
    const requestKey = `${requestedPage}:${requestedSort}:${requestedQuery}`;
    try {
      const params = new URLSearchParams({
        limit: String(SCORE_PAGE_SIZE),
        page: String(requestedPage),
        sort: requestedSort,
      });
      if (requestedQuery) params.set("q", requestedQuery);
      const response = await fetch(`${API_BASE}/api/v1/scoreboard?${params}`, {
        headers: authHeaders(),
        signal: requestController?.signal,
      });
      if (!response.ok) throw response.status;
      const nextETag = response.headers.get("etag") ?? "";
      if (nextETag && nextETag === scoreETag && requestKey === scoreETagKey) {
        scoresLoaded = true;
        error = "";
        scoreStale = false;
        return;
      }
      const scoreboard = await response.json();
      if (
        (view !== "standings" && requestKey !== "1:rank:") ||
        (view === "standings" &&
          (requestedPage !== scorePage ||
            requestedQuery !== search.trim() ||
            requestedSort !== sortKey))
      ) {
        scoreReloadPending = true;
        return;
      }

      const nextEntries: Entry[] = scoreboard.leaderboard ?? [];
      const nextTotalUsers = scoreboard.total_users ?? nextEntries.length;
      const nextMatchingUsers = scoreboard.matching_users ?? nextTotalUsers;
      const nextPages = Math.max(
        1,
        Math.ceil(nextMatchingUsers / SCORE_PAGE_SIZE),
      );
      if (view === "standings" && requestedPage > nextPages) {
        scorePage = nextPages;
        scoreReloadPending = true;
        return;
      }

      entries = nextEntries;
      totalUsers = nextTotalUsers;
      matchingUsers = nextMatchingUsers;
      scoreETag = nextETag;
      scoreETagKey = requestKey;
      scoresLoaded = true;
      error = "";
      scoreStale = false;
    } catch (caught) {
      if (requestController?.signal.aborted) return;
      if (!scoresLoaded) {
        error =
          typeof caught === "number"
            ? `HTTP ${caught}`
            : "Failed to load scoreboard";
      } else {
        scoreStale = true;
      }
    } finally {
      loading = false;
      scoreInFlight = false;
      if (scoreReloadPending) {
        scoreReloadPending = false;
        void loadScores();
      }
    }
  }

  function scheduleSearch() {
    if (view !== "standings") return;
    scorePage = 1;
    clearTimeout(searchDebounce);
    searchDebounce = setTimeout(loadScores, 250);
  }

  function setView(next: "matrix" | "standings") {
    view = next;
    scorePage = 1;
    if (next === "matrix") void loadMatrix(true);
    void loadScores();
  }

  function setSort(next: "rank" | "name") {
    sortKey = next;
    if (view === "standings") {
      scorePage = 1;
      void loadScores();
    }
  }

  function setScorePage(next: number) {
    scorePage = next;
    void loadScores();
  }

  async function loadHistory() {
    if (historyInFlight || document.hidden) return;
    historyInFlight = true;
    try {
      const response = await fetch(`${API_BASE}/api/v1/scoreboard/history`, {
        headers: authHeaders(),
        signal: requestController?.signal,
      });
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      historyStale = false;
      historyError = "";
      const nextETag = response.headers.get("etag") ?? "";
      if (nextETag && nextETag === historyETag) return;
      const history = await response.json();
      const historySeries: HistorySeries[] = history.series ?? [];
      const usedColors = new Set<string>();
      raceSeries = historySeries.map((series) => {
        const color = uniqueSeriesColor(series.id, usedColors);
        usedColors.add(color);
        return { label: series.label, color, points: series.points };
      });
      historyETag = nextETag;
    } catch {
      if (!requestController?.signal.aborted) {
        if (raceSeries.length > 0) historyStale = true;
        else historyError = "Score history is temporarily unavailable.";
      }
    } finally {
      historyInFlight = false;
    }
  }

  async function loadMatrix(force = false) {
    if (matrixInFlight || document.hidden || (!force && view !== "matrix"))
      return;
    matrixInFlight = true;
    try {
      const response = await fetch(`${API_BASE}/api/v1/scoreboard/matrix`, {
        headers: authHeaders(),
        signal: requestController?.signal,
      });
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      const nextETag = response.headers.get("etag") ?? "";
      if (nextETag && nextETag === matrixETag) {
        matrixError = "";
        return;
      }
      const data = await response.json();
      matrixChallenges = data.challenges ?? [];
      matrixRows = data.rows ?? [];
      matrixTotalUsers = data.total_users ?? matrixRows.length;
      matrixETag = nextETag;
      matrixError = "";
    } catch (caught) {
      if (requestController?.signal.aborted) return;
      matrixError =
        caught instanceof Error
          ? caught.message
          : "Failed to load challenge matrix";
    } finally {
      matrixLoading = false;
      matrixInFlight = false;
    }
  }

  function scheduleScores() {
    scoreTimer = setTimeout(async () => {
      await loadScores();
      scheduleScores();
    }, jitter(SCORE_POLL_MS));
  }

  function scheduleHistory() {
    historyTimer = setTimeout(async () => {
      await loadHistory();
      scheduleHistory();
    }, jitter(HISTORY_POLL_MS));
  }

  function scheduleMatrix() {
    matrixTimer = setTimeout(async () => {
      await loadMatrix();
      scheduleMatrix();
    }, jitter(MATRIX_POLL_MS));
  }

  onMount(() => {
    requestController = new AbortController();
    loadScores();
    loadHistory();
    loadMatrix(true);
    scheduleScores();
    scheduleHistory();
    scheduleMatrix();
    const refreshVisible = () => {
      if (!document.hidden) {
        loadScores();
        loadHistory();
        loadMatrix(view === "matrix");
      }
    };
    document.addEventListener("visibilitychange", refreshVisible);
    return () => {
      clearTimeout(scoreTimer);
      clearTimeout(historyTimer);
      clearTimeout(matrixTimer);
      clearTimeout(searchDebounce);
      requestController.abort();
      document.removeEventListener("visibilitychange", refreshVisible);
    };
  });

  const displayName = (entry: Entry) => entry.display_name || entry.username;

  const raceColors = [
    "#6f9dc9",
    "#7bb587",
    "#cf7f83",
    "#4faaa6",
    "#a394c9",
    "#c9b46e",
    "#cf9268",
    "#79c7cf",
    "#9aa657",
    "#bd85b0",
    "#8f93d6",
    "#b39a86",
  ];

  function uniqueSeriesColor(id: string, used: Set<string>): string {
    const preferred = teamColor(id);
    if (!used.has(preferred)) return preferred;
    const start = Math.max(0, raceColors.indexOf(preferred));
    for (let offset = 1; offset < raceColors.length; offset += 1) {
      const candidate = raceColors[(start + offset) % raceColors.length];
      if (!used.has(candidate)) return candidate;
    }
    return preferred;
  }

  $: leaderIndex = raceSeries.reduce((best, series, index) => {
    if (best < 0) return index;
    const score = series.points.at(-1)?.y ?? 0;
    const bestScore = raceSeries[best].points.at(-1)?.y ?? 0;
    return score > bestScore ? index : best;
  }, -1);
  $: raceStart = raceSeries.length
    ? Math.min(
        ...raceSeries.flatMap((series) =>
          series.points.map((point) => point.x),
        ),
      )
    : 0;
  $: raceTime = (value: number) =>
    value <= raceStart ? "0" : `+${formatDur(value - raceStart)}`;
  $: matrixCategories = [
    ...new Set(matrixChallenges.map((challenge) => challenge.category)),
  ].sort((a, b) => a.localeCompare(b));
  $: filtered = (() => {
    return entries;
  })();
  $: scorePages = Math.max(1, Math.ceil(matchingUsers / SCORE_PAGE_SIZE));

  function formatDate(value?: string) {
    return formatLocalDateTime(value);
  }

  function tierIcon(rank: number): string | null {
    if (rank === 1) return "mdi:trophy";
    if (rank <= 3) return "mdi:medal";
    return null;
  }

  function share(entry: Entry) {
    downloadRankCard({
      rank: entry.rank,
      username: displayName(entry),
      score: entry.total_score,
      solves: entry.challenges_solved,
      delta: entry.delta,
      spark: entry.spark ?? [],
      color: teamColor(entry.user_id),
    });
  }
</script>

<svelte:head>
  <title>Scoreboard - Anvil</title>
</svelte:head>

<div class="max-w-[1600px] mx-auto px-4 sm:px-6 lg:px-8 py-8">
  <PageHeader title="Scoreboard" subtitle="{totalUsers} participants">
    <div
      slot="actions"
      class="flex w-full max-w-full flex-wrap items-center justify-start gap-2 sm:w-auto sm:justify-end"
    >
      <div class="relative w-full sm:w-auto">
        <Icon
          icon="mdi:magnify"
          class="absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-stone-600"
        />
        <input
          bind:value={search}
          type="search"
          placeholder="Search players"
          aria-label="Search scoreboard"
          maxlength="50"
          on:input={scheduleSearch}
          class="w-full rounded-md border border-stone-800 bg-stone-900/60 py-1.5 pl-8 pr-3 text-sm text-stone-200 placeholder-stone-600 focus:border-stone-700 focus:outline-none sm:w-52"
        />
      </div>

      <div
        class="flex overflow-hidden rounded-md border border-stone-800 text-xs"
      >
        <button
          class="inline-flex items-center gap-1.5 px-2.5 py-1.5 transition-colors {view ===
          'matrix'
            ? 'bg-stone-800 text-stone-200'
            : 'text-stone-500 hover:text-stone-300'}"
          on:click={() => setView("matrix")}
          aria-pressed={view === "matrix"}
        >
          <OpticalIcon icon="mdi:view-grid-outline" size={12} box={12} />
          <span class="optical-label">Matrix</span>
        </button>
        <button
          class="inline-flex items-center gap-1.5 border-l border-stone-800 px-2.5 py-1.5 transition-colors {view ===
          'standings'
            ? 'bg-stone-800 text-stone-200'
            : 'text-stone-500 hover:text-stone-300'}"
          on:click={() => setView("standings")}
          aria-pressed={view === "standings"}
        >
          <OpticalIcon icon="mdi:format-list-numbered" size={12} box={12} />
          <span class="optical-label">Standings</span>
        </button>
      </div>

      {#if view === "matrix"}
        <select
          bind:value={category}
          aria-label="Filter matrix by category"
          class="max-w-44 rounded-md border border-stone-800 bg-stone-900/60 px-2.5 py-1.5 text-xs text-stone-300 focus:border-stone-700 focus:outline-none"
        >
          <option value="">All categories</option>
          {#each matrixCategories as matrixCategory}
            <option value={matrixCategory}>{matrixCategory}</option>
          {/each}
        </select>
      {/if}

      <div
        class="flex overflow-hidden rounded-md border border-stone-800 text-xs"
      >
        <button
          class="px-2.5 py-1.5 transition-colors {sortKey === 'rank'
            ? 'bg-stone-800 text-stone-200'
            : 'text-stone-500 hover:text-stone-300'}"
          on:click={() => setSort("rank")}
          aria-pressed={sortKey === "rank"}
        >
          Rank
        </button>
        <button
          class="border-l border-stone-800 px-2.5 py-1.5 transition-colors {sortKey ===
          'name'
            ? 'bg-stone-800 text-stone-200'
            : 'text-stone-500 hover:text-stone-300'}"
          on:click={() => setSort("name")}
          aria-pressed={sortKey === "name"}
        >
          Name
        </button>
      </div>
    </div>
  </PageHeader>

  {#if loading}
    <div class="flex items-center justify-center py-16">
      <Icon icon="mdi:loading" class="h-6 w-6 animate-spin text-stone-500" />
    </div>
  {:else if error}
    <EmptyState icon="mdi:alert-circle-outline" text={error}>
      <button
        on:click={loadScores}
        class="mt-3 text-xs text-amber-500 hover:text-amber-400">Retry</button
      >
    </EmptyState>
  {:else if totalUsers === 0}
    <EmptyState icon="mdi:trophy-outline" text="No scores yet." />
  {:else}
    {#if scoreStale}
      <div
        role="status"
        class="mb-4 rounded-md border border-warn/25 bg-warn/5 px-3 py-2 text-xs text-warn"
      >
        Live standings update delayed; showing the last successful result.
      </div>
    {/if}
    {#if raceSeries.length}
      <div class="mb-6">
        <Card title="Score over time">
          <span
            slot="meta"
            class="metadata-label {historyStale
              ? 'text-warn'
              : 'text-stone-500'}"
          >
            {historyStale ? "Update delayed" : `Top ${raceSeries.length}`}
          </span>
          <div class="mb-3 flex flex-wrap gap-x-4 gap-y-2">
            {#each raceSeries as series}
              <span
                class="inline-flex items-center gap-1.5 text-xs leading-none text-stone-500"
              >
                <span
                  class="h-2 w-2 shrink-0 rounded-full"
                  style="background: {series.color};"
                ></span>
                <span class="optical-label max-w-32 truncate"
                  >{series.label}</span
                >
              </span>
            {/each}
          </div>
          <LineChart
            series={raceSeries}
            height={260}
            curve="step"
            emphasize={leaderIndex}
            xFormat={raceTime}
          />
        </Card>
      </div>
    {:else if historyError}
      <div
        role="status"
        class="mb-6 flex items-center justify-between gap-3 rounded-md border border-stone-800 bg-stone-900/20 px-3 py-2.5 text-xs text-stone-500"
      >
        <span>{historyError}</span>
        <button
          type="button"
          on:click={loadHistory}
          class="shrink-0 text-amber-500 transition-colors hover:text-amber-400"
          >Retry</button
        >
      </div>
    {/if}

    {#if view === "matrix"}
      <Card title="Challenge matrix" bodyClass="p-0">
        <span slot="meta" class="metadata-label text-stone-500"
          >{matrixChallenges.length} challenges</span
        >
        {#if matrixLoading && matrixRows.length === 0}
          <div
            class="flex items-center justify-center py-12"
            role="status"
            aria-label="Loading challenge matrix"
          >
            <Icon
              icon="mdi:loading"
              class="h-5 w-5 animate-spin text-stone-500"
            />
          </div>
        {:else if matrixError && matrixRows.length === 0}
          <div class="px-4 py-10 text-center">
            <p class="text-sm text-stone-500">
              The challenge matrix is temporarily unavailable.
            </p>
            <button
              on:click={() => loadMatrix(true)}
              class="mt-3 text-xs text-amber-500 hover:text-amber-400"
              >Retry</button
            >
          </div>
        {:else}
          {#if matrixError}
            <div
              role="status"
              class="border-b border-warn/20 bg-warn/5 px-4 py-2 text-xs text-warn"
            >
              Matrix update delayed; showing the last successful result.
            </div>
          {/if}
          <ScoreboardMatrix
            challenges={matrixChallenges}
            rows={matrixRows}
            totalPlayers={matrixTotalUsers}
            {search}
            {category}
            sort={sortKey}
          />
        {/if}
      </Card>
    {:else}
      <Card title="Standings" bodyClass="">
        <span slot="meta" class="metadata-label text-stone-500"
          >{matchingUsers.toLocaleString()} players</span
        >
        <div class="overflow-x-auto">
          <table class="w-full min-w-[680px] text-sm">
            <thead>
              <tr
                class="metadata-label border-b border-stone-800 text-stone-500"
              >
                <th class="w-16 px-4 py-2.5 text-left">Rank</th>
                <th class="px-4 py-2.5 text-left">Player</th>
                <th class="hidden w-28 px-4 py-2.5 text-left sm:table-cell"
                  >Trend</th
                >
                <th class="hidden px-4 py-2.5 text-right md:table-cell"
                  >Solves</th
                >
                <th class="px-4 py-2.5 text-right">Score</th>
                <th class="hidden px-4 py-2.5 text-right xl:table-cell"
                  >Last solve</th
                >
                <th class="w-10 px-3 py-2.5"></th>
              </tr>
            </thead>
            <tbody>
              {#each filtered as entry (entry.user_id)}
                {@const color = teamColor(entry.user_id)}
                {@const icon = tierIcon(entry.rank)}
                <tr
                  class="group border-b border-stone-800/60 transition-colors hover:bg-stone-800/20 {entry.rank ===
                  1
                    ? 'bg-amber-500/[0.04]'
                    : ''}"
                >
                  <td class="whitespace-nowrap px-4 py-2.5">
                    <div class="flex h-4 items-center gap-1.5 leading-none">
                      {#if icon}
                        <OpticalIcon
                          {icon}
                          size={14}
                          box={16}
                          className={rankAccent(entry.rank)}
                        />
                      {:else}
                        <span class="h-4 w-4 shrink-0"></span>
                      {/if}
                      <span
                        class="optical-label font-semibold tabular-nums leading-[14px] text-stone-200"
                        >{entry.rank}</span
                      >
                      {#if entry.delta !== 0}
                        <span
                          class="{entry.delta > 0
                            ? 'text-up'
                            : 'text-down'} inline-flex items-center text-[0.65rem] leading-none tabular-nums"
                        >
                          <OpticalIcon
                            icon={entry.delta > 0
                              ? "mdi:menu-up"
                              : "mdi:menu-down"}
                            size={12}
                            box={12}
                          />
                          <span class="optical-label"
                            >{Math.abs(entry.delta)}</span
                          >
                        </span>
                      {/if}
                    </div>
                  </td>
                  <td class="whitespace-nowrap px-4 py-2.5">
                    <div class="flex items-center gap-2.5 leading-none">
                      <span
                        class="h-2 w-2 shrink-0 rounded-full"
                        style="background: {color};"
                      ></span>
                      <a
                        href="/profile/{entry.username}"
                        class="optical-label max-w-[200px] truncate text-stone-200 transition hover:text-amber-400"
                        >{displayName(entry)}</a
                      >
                    </div>
                  </td>
                  <td class="hidden px-4 py-2.5 sm:table-cell">
                    <Sparkline data={entry.spark ?? []} {color} />
                  </td>
                  <td
                    class="hidden whitespace-nowrap px-4 py-2.5 text-right tabular-nums text-stone-400 md:table-cell"
                    >{entry.challenges_solved}</td
                  >
                  <td
                    class="whitespace-nowrap px-4 py-2.5 text-right font-semibold tabular-nums text-amber-500/90"
                    >{entry.total_score.toLocaleString()}</td
                  >
                  <td
                    class="hidden whitespace-nowrap px-4 py-2.5 text-right text-xs tabular-nums text-stone-500 xl:table-cell"
                    title={instantTitle(entry.last_solve_at)}
                    >{formatDate(entry.last_solve_at)}</td
                  >
                  <td class="px-3 py-2.5 text-right">
                    <button
                      on:click={() => share(entry)}
                      title="Share rank card"
                      aria-label="Share {displayName(entry)} rank card"
                      class="inline-flex h-8 w-8 items-center justify-center text-stone-700 opacity-0 transition hover:text-amber-400 focus:opacity-100 group-hover:opacity-100"
                    >
                      <Icon icon="mdi:share-variant-outline" class="h-4 w-4" />
                    </button>
                  </td>
                </tr>
              {/each}
              {#if filtered.length === 0}
                <tr>
                  <td colspan="7" class="px-4 py-8 text-center text-stone-500"
                    >No players match “{search}”.</td
                  >
                </tr>
              {/if}
            </tbody>
          </table>
        </div>
        {#if scorePages > 1}
          <div
            class="mt-4 flex items-center justify-between border-t border-stone-800 pt-4 text-xs text-stone-500"
          >
            <span class="metadata-label">Page {scorePage} of {scorePages}</span>
            <div
              class="inline-flex overflow-hidden rounded border border-stone-800"
            >
              <button
                type="button"
                disabled={scorePage === 1}
                on:click={() => setScorePage(scorePage - 1)}
                class="px-3 py-1.5 hover:text-stone-200 disabled:cursor-not-allowed disabled:opacity-30"
                >Previous</button
              >
              <button
                type="button"
                disabled={scorePage === scorePages}
                on:click={() => setScorePage(scorePage + 1)}
                class="border-l border-stone-800 px-3 py-1.5 hover:text-stone-200 disabled:cursor-not-allowed disabled:opacity-30"
                >Next</button
              >
            </div>
          </div>
        {/if}
      </Card>
    {/if}
  {/if}
</div>
