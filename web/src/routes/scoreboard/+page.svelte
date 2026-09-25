<script lang="ts">
  import Icon from "@iconify/svelte";
  import { onMount } from "svelte";
  import { API_BASE } from "$lib/config";
  import { auth } from "$stores/auth";
  import { platformInfo } from "$lib/stores/platform";
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
    difficulty?: string;
    points: number;
  }

  interface MatrixRow {
    rank: number;
    user_id: string;
    username: string;
    name: string;
    total: number;
    delta: number;
    cells: { s: number; b: number; p?: number }[];
  }

  type View = "standings" | "matrix";

  // only the open tab polls; the api caches each page for 2-5s per pod
  const SCORE_POLL_MS = 15000;
  const MATRIX_POLL_MS = 20000;
  const HISTORY_POLL_MS = 30000;
  const SCORE_PAGE_SIZE = 100;
  const MATRIX_PAGE_SIZE = 50;

  let entries: Entry[] = [];
  let raceSeries: Series[] = [];
  let matrixChallenges: MatrixChallenge[] = [];
  let matrixRows: MatrixRow[] = [];
  let total = 0;
  let scoreMatching = 0;
  let matrixMatching = 0;
  let boardTeams: boolean | null = null;
  let economy = false;
  let frozen = false;
  let loaded: Record<View, boolean> = { standings: false, matrix: false };
  let loadError = 0; // http status of a failed first load (-1 = network)
  let boardStale = false;
  let historyStale = false;
  let historyError = "";
  let boardSeq = 0;
  let boardBusy = false;
  let historyInFlight = false;
  let boardTimer: ReturnType<typeof setTimeout>;
  let historyTimer: ReturnType<typeof setTimeout>;
  let requestController: AbortController;
  let searchDebounce: ReturnType<typeof setTimeout>;
  const lastETag: Record<View, string> = { standings: "", matrix: "" };
  const lastURL: Record<View, string> = { standings: "", matrix: "" };
  let historyETag = "";
  let historyRaw: HistorySeries[] = [];

  let search = "";
  let sortKey: "rank" | "name" = "rank";
  let view: View = "standings";
  let category = "";
  let scorePage = 1;
  let matrixPage = 1;

  const jitter = (interval: number) => interval * (0.85 + Math.random() * 0.3);
  function authHeaders(): Record<string, string> {
    if (typeof localStorage === "undefined") return {};
    const token = localStorage.getItem("accessToken");
    return token ? { Authorization: `Bearer ${token}` } : {};
  }

  function boardURL(which: View) {
    const params = new URLSearchParams({
      page: String(which === "matrix" ? matrixPage : scorePage),
      limit: String(which === "matrix" ? MATRIX_PAGE_SIZE : SCORE_PAGE_SIZE),
      sort: sortKey,
    });
    const query = search.trim();
    if (query) params.set("q", query);
    return `${API_BASE}/api/v1/scoreboard${which === "matrix" ? "/matrix" : ""}?${params}`;
  }

  // loads the open tab; a newer request (tab, page, search or sort change) supersedes an older one
  async function loadBoard(force = false) {
    if (typeof document !== "undefined" && document.hidden) return;
    if (boardBusy && !force) return;
    const seq = ++boardSeq;
    const which = view;
    const url = boardURL(which);
    boardBusy = true;
    try {
      const response = await fetch(url, {
        headers: authHeaders(),
        signal: requestController?.signal,
      });
      if (seq !== boardSeq) return;
      if (!response.ok) throw response.status;
      const etag = response.headers.get("etag") ?? "";
      if (etag && etag === lastETag[which] && url === lastURL[which]) {
        loadError = 0;
        boardStale = false;
        return;
      }
      const data = await response.json();
      if (seq !== boardSeq) return;

      const size = which === "matrix" ? MATRIX_PAGE_SIZE : SCORE_PAGE_SIZE;
      const matching = data.matching_users ?? data.total_users ?? 0;
      const pages = Math.max(1, Math.ceil(matching / size));
      const requested = which === "matrix" ? matrixPage : scorePage;
      if (requested > pages) {
        if (which === "matrix") matrixPage = pages;
        else scorePage = pages;
        setTimeout(() => loadBoard(true));
        return;
      }

      frozen = !!data.frozen;
      boardTeams = !!data.teams;
      economy = !!data.economy;
      total = data.total_users ?? 0;
      if (which === "matrix") {
        matrixChallenges = data.challenges ?? [];
        matrixRows = data.rows ?? [];
        matrixMatching = matching;
      } else {
        entries = data.leaderboard ?? [];
        scoreMatching = matching;
        if (!boardTeams) {
          const viewer = entries.find((entry) => entry.user_id === $auth.user?.id);
          if (viewer) auth.updateRank(viewer.rank);
        }
      }
      lastETag[which] = etag;
      lastURL[which] = url;
      loaded = { ...loaded, [which]: true };
      loadError = 0;
      boardStale = false;
    } catch (caught) {
      if (requestController?.signal.aborted || seq !== boardSeq) return;
      if (loaded[which]) boardStale = true;
      else loadError = typeof caught === "number" ? caught : -1;
    } finally {
      if (seq === boardSeq) boardBusy = false;
    }
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
      if (nextETag && nextETag === historyETag) {
        historyRaw = historyRaw; // re-extend the lines to now
        return;
      }
      const history = await response.json();
      historyRaw = history.series ?? [];
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

  function reloadBoard() {
    clearTimeout(boardTimer);
    void loadBoard(true);
    scheduleBoard();
  }

  function scheduleSearch() {
    scorePage = 1;
    matrixPage = 1;
    clearTimeout(searchDebounce);
    searchDebounce = setTimeout(reloadBoard, 250);
  }

  function setView(next: View) {
    if (view === next) return;
    view = next;
    boardStale = false;
    reloadBoard();
  }

  function setSort(next: "rank" | "name") {
    if (sortKey === next) return;
    sortKey = next;
    scorePage = 1;
    matrixPage = 1;
    reloadBoard();
  }

  function setScorePage(next: number) {
    scorePage = next;
    reloadBoard();
  }

  function setMatrixPage(next: number) {
    matrixPage = next;
    reloadBoard();
  }

  function scheduleBoard() {
    clearTimeout(boardTimer);
    boardTimer = setTimeout(
      async () => {
        await loadBoard();
        scheduleBoard();
      },
      jitter(view === "matrix" ? MATRIX_POLL_MS : SCORE_POLL_MS),
    );
  }

  function scheduleHistory() {
    historyTimer = setTimeout(async () => {
      await loadHistory();
      scheduleHistory();
    }, jitter(HISTORY_POLL_MS));
  }

  onMount(() => {
    requestController = new AbortController();
    void loadBoard(true);
    void loadHistory();
    scheduleBoard();
    scheduleHistory();
    const refreshVisible = () => {
      if (!document.hidden) {
        void loadBoard(true);
        void loadHistory();
      }
    };
    document.addEventListener("visibilitychange", refreshVisible);
    return () => {
      clearTimeout(boardTimer);
      clearTimeout(historyTimer);
      clearTimeout(searchDebounce);
      requestController.abort();
      document.removeEventListener("visibilitychange", refreshVisible);
    };
  });

  const displayName = (entry: Entry) => entry.display_name || entry.username;
  const score = (value: number) => Math.round(value).toLocaleString();

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

  function seconds(value?: string) {
    const parsed = value ? Date.parse(value) / 1000 : NaN;
    return Number.isFinite(parsed) ? parsed : null;
  }

  // every line starts at 0 when the event opens and runs flat to now, so a single solve still draws
  $: eventStart = seconds($platformInfo?.event?.start_at);
  $: eventEnd = seconds($platformInfo?.event?.end_at);
  $: raceSeries = (() => {
    const points = historyRaw.flatMap((series) => series.points);
    if (!points.length) return [];
    const firstX = Math.min(...points.map((point) => point.x));
    const lastX = Math.max(...points.map((point) => point.x));
    const origin = eventStart != null && eventStart < firstX ? eventStart : firstX;
    const now = Math.min(Date.now() / 1000, eventEnd ?? Infinity);
    const edge = Math.max(lastX, now);
    const used = new Set<string>();
    return historyRaw.map((series) => {
      const color = uniqueSeriesColor(series.id, used);
      used.add(color);
      const last = series.points.at(-1);
      return {
        label: series.label,
        color,
        points: [
          { x: origin, y: 0 },
          ...series.points,
          ...(last && last.x < edge ? [{ x: edge, y: last.y }] : []),
        ],
      };
    });
  })();
  $: leaderIndex = raceSeries.reduce((best, series, index) => {
    if (best < 0) return index;
    const score = series.points.at(-1)?.y ?? 0;
    const bestScore = raceSeries[best].points.at(-1)?.y ?? 0;
    return score > bestScore ? index : best;
  }, -1);
  $: raceStart = raceSeries.length ? raceSeries[0].points[0].x : 0;
  $: raceTime = (value: number) =>
    value <= raceStart ? "0" : `+${formatDur(value - raceStart)}`;
  $: matrixCategories = [
    ...new Set(matrixChallenges.map((challenge) => challenge.category)),
  ].sort((a, b) => a.localeCompare(b));
  $: scorePages = Math.max(1, Math.ceil(scoreMatching / SCORE_PAGE_SIZE));
  $: matrixPages = Math.max(1, Math.ceil(matrixMatching / MATRIX_PAGE_SIZE));
  $: anyLoaded = loaded.standings || loaded.matrix;
  // the api says whether rows are teams; /info covers the first paint
  $: teamsBoard =
    boardTeams ?? !!($platformInfo?.teams_mode || $platformInfo?.economy_enabled);
  $: noun = teamsBoard ? "team" : "player";
  $: subtitle = !anyLoaded
    ? ""
    : teamsBoard
      ? total === 0
        ? "Waiting for the first solve"
        : `${total.toLocaleString()} team${total === 1 ? "" : "s"} on the board`
      : `${total.toLocaleString()} participant${total === 1 ? "" : "s"}`;
  $: errorText =
    loadError === 401
      ? "Sign in to see the scoreboard."
      : loadError === 404
        ? "The scoreboard is hidden right now."
        : loadError === -1
          ? "Couldn't reach the scoreboard."
          : `Couldn't load the scoreboard (HTTP ${loadError}).`;

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
      score: Math.round(entry.total_score),
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

<div class="w-full px-4 sm:px-6 lg:px-8 2xl:px-10 py-8">
  {#if frozen}
    <div class="mb-4 flex items-center gap-2 rounded-md border border-amber-500/20 bg-amber-500/[0.07] px-3 py-2 text-sm text-amber-500">
      <Icon icon="mdi:snowflake" class="h-4 w-4 shrink-0" />
      Scoreboard frozen - final standings are hidden until the results are published.
    </div>
  {/if}
  <PageHeader title="Scoreboard" {subtitle}>
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
          placeholder={teamsBoard ? "Search teams" : "Search players"}
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
          'standings'
            ? 'bg-stone-800 text-stone-200'
            : 'text-stone-500 hover:text-stone-300'}"
          on:click={() => setView("standings")}
          aria-pressed={view === "standings"}
        >
          <OpticalIcon icon="mdi:format-list-numbered" size={12} box={12} />
          <span class="optical-label">Standings</span>
        </button>
        <button
          class="inline-flex items-center gap-1.5 border-l border-stone-800 px-2.5 py-1.5 transition-colors {view ===
          'matrix'
            ? 'bg-stone-800 text-stone-200'
            : 'text-stone-500 hover:text-stone-300'}"
          on:click={() => setView("matrix")}
          aria-pressed={view === "matrix"}
        >
          <OpticalIcon icon="mdi:view-grid-outline" size={12} box={12} />
          <span class="optical-label">Matrix</span>
        </button>
      </div>

      {#if view === "matrix" && matrixCategories.length > 1}
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

  {#if !anyLoaded && !loadError}
    <div
      class="flex items-center justify-center py-16"
      role="status"
      aria-label="Loading scoreboard"
    >
      <Icon icon="mdi:loading" class="h-6 w-6 animate-spin text-stone-500" />
    </div>
  {:else if !anyLoaded}
    <Card hasHeader={false}>
      <EmptyState
        icon={loadError === 401
          ? "mdi:lock-outline"
          : loadError === 404
            ? "mdi:eye-off-outline"
            : "mdi:alert-circle-outline"}
        text={errorText}
      >
        {#if loadError === 401}
          <a href="/login" class="mt-3 text-xs text-amber-500 hover:text-amber-400"
            >Sign in</a
          >
        {:else if loadError !== 404}
          <button
            on:click={() => loadBoard(true)}
            class="mt-3 text-xs text-amber-500 hover:text-amber-400">Retry</button
          >
        {/if}
      </EmptyState>
    </Card>
  {:else if total === 0}
    <Card title={view === "matrix" ? "Challenge matrix" : "Standings"}>
      <EmptyState
        icon="mdi:trophy-outline"
        text="No solves yet."
      />
    </Card>
  {:else}
    {#if boardStale}
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
                <span class="optical-label max-w-44 truncate sm:max-w-60" title={series.label}
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
        {#if !loaded.matrix}
          <div
            class="flex items-center justify-center py-12"
            role="status"
            aria-label="Loading challenge matrix"
          >
            {#if loadError}
              <div class="text-center">
                <p class="text-sm text-stone-500">
                  The challenge matrix is temporarily unavailable.
                </p>
                <button
                  on:click={() => loadBoard(true)}
                  class="mt-3 text-xs text-amber-500 hover:text-amber-400"
                  >Retry</button
                >
              </div>
            {:else}
              <Icon
                icon="mdi:loading"
                class="h-5 w-5 animate-spin text-stone-500"
              />
            {/if}
          </div>
        {:else}
          <ScoreboardMatrix
            challenges={matrixChallenges}
            rows={matrixRows}
            teams={teamsBoard}
            {economy}
            {search}
            {category}
            page={matrixPage}
            pages={matrixPages}
            matching={matrixMatching}
            onPage={setMatrixPage}
          />
        {/if}
      </Card>
    {:else}
      <Card title="Standings" bodyClass="p-0">
        <span slot="meta" class="metadata-label text-stone-500"
          >{scoreMatching.toLocaleString()} {noun}{scoreMatching === 1 ? "" : "s"}</span
        >
        {#if !loaded.standings}
          <div
            class="flex items-center justify-center py-12"
            role="status"
            aria-label="Loading standings"
          >
            {#if loadError}
              <div class="text-center">
                <p class="text-sm text-stone-500">
                  Standings are temporarily unavailable.
                </p>
                <button
                  on:click={() => loadBoard(true)}
                  class="mt-3 text-xs text-amber-500 hover:text-amber-400"
                  >Retry</button
                >
              </div>
            {:else}
              <Icon
                icon="mdi:loading"
                class="h-5 w-5 animate-spin text-stone-500"
              />
            {/if}
          </div>
        {:else}
          <div class="overflow-x-auto">
            <table class="w-full table-fixed text-sm">
              <thead>
                <tr
                  class="metadata-label border-b border-stone-800 text-stone-500"
                >
                  <th class="w-[5.25rem] py-2.5 pl-3 pr-2 text-left sm:w-28 sm:pl-4"
                    >Rank</th
                  >
                  <th class="px-2 py-2.5 text-left sm:px-4"
                    >{teamsBoard ? "Team" : "Player"}</th
                  >
                  {#if !teamsBoard}
                    <th class="hidden w-32 px-4 py-2.5 text-left md:table-cell"
                      >Trend</th
                    >
                  {/if}
                  <th class="hidden w-24 px-4 py-2.5 text-right sm:table-cell"
                    >Solves</th
                  >
                  <th class="w-24 py-2.5 pl-2 pr-3 text-right sm:w-28 sm:px-4"
                    >Score</th
                  >
                  <th class="hidden w-48 px-4 py-2.5 text-right lg:table-cell"
                    >Last solve</th
                  >
                  <th class="hidden w-12 px-3 py-2.5 sm:table-cell"
                    ><span class="sr-only">Share</span></th
                  >
                </tr>
              </thead>
              <tbody>
                {#each entries as entry (entry.user_id)}
                  {@const color = teamColor(entry.user_id)}
                  {@const icon = tierIcon(entry.rank)}
                  <tr
                    class="group border-b border-stone-800/60 transition-colors last:border-b-0 hover:bg-stone-800/20 {entry.rank ===
                    1
                      ? 'bg-amber-500/[0.04]'
                      : ''}"
                  >
                    <td class="whitespace-nowrap py-2.5 pl-3 pr-2 sm:pl-4">
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
                    <td class="whitespace-nowrap px-2 py-2.5 sm:px-4">
                      <div class="flex min-w-0 items-center gap-2.5 leading-none">
                        <span
                          class="h-2 w-2 shrink-0 rounded-full"
                          style="background: {color};"
                        ></span>
                        {#if teamsBoard}
                          <span
                            class="optical-label min-w-0 truncate text-stone-200"
                            title={entry.username}>{entry.username}</span
                          >
                        {:else}
                          <a
                            href="/profile/{encodeURIComponent(entry.username)}"
                            class="optical-label min-w-0 truncate text-stone-200 transition hover:text-amber-400"
                            title={displayName(entry)}>{displayName(entry)}</a
                          >
                        {/if}
                      </div>
                    </td>
                    {#if !teamsBoard}
                      <td class="hidden px-4 py-2.5 md:table-cell">
                        <Sparkline data={entry.spark ?? []} {color} />
                      </td>
                    {/if}
                    <td
                      class="hidden whitespace-nowrap px-4 py-2.5 text-right tabular-nums text-stone-400 sm:table-cell"
                      >{entry.challenges_solved}</td
                    >
                    <td
                      class="whitespace-nowrap py-2.5 pl-2 pr-3 text-right font-semibold tabular-nums text-amber-500/90 sm:px-4"
                      >{score(entry.total_score)}</td
                    >
                    <td
                      class="hidden whitespace-nowrap px-4 py-2.5 text-right text-xs tabular-nums text-stone-500 lg:table-cell"
                      title={instantTitle(entry.last_solve_at)}
                      >{entry.last_solve_at ? formatDate(entry.last_solve_at) : "-"}</td
                    >
                    <td class="hidden px-3 py-1.5 text-right sm:table-cell">
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
                {#if entries.length === 0}
                  <tr>
                    <td colspan="7" class="px-4 py-10 text-center text-stone-500"
                      >{search.trim()
                        ? `No ${noun}s match “${search.trim()}”.`
                        : `No ${noun}s on this page.`}</td
                    >
                  </tr>
                {/if}
              </tbody>
            </table>
          </div>
          {#if scorePages > 1}
            <div
              class="flex items-center justify-between border-t border-stone-800 px-4 py-3 text-xs text-stone-500"
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
        {/if}
      </Card>
    {/if}
  {/if}
</div>
