<script lang="ts">
  import Icon from "@iconify/svelte";
  import { onDestroy, onMount } from "svelte";
  import { page } from "$app/stores";
  import { API_BASE } from "$lib/config";
  import { categoryColor } from "$lib/rank";
  import { formatDur } from "$lib/chart/time";
  import {
    formatLocalDate,
    formatLocalTimeWithZone,
    instantTitle,
    parseInstant,
  } from "$lib/time";
  import Card from "$lib/components/Card.svelte";
  import StatTile from "$lib/components/StatTile.svelte";
  import EmptyState from "$lib/components/EmptyState.svelte";
  import ProfileScoreChart from "$lib/components/ProfileScoreChart.svelte";
  import CategoryBars from "$lib/components/CategoryBars.svelte";
  import SolveTimeline from "$lib/components/SolveTimeline.svelte";
  import SolveCadence from "$lib/components/SolveCadence.svelte";
  import DifficultyBars from "$lib/components/DifficultyBars.svelte";
  import OpticalIcon from "$lib/components/OpticalIcon.svelte";

  interface Solve {
    name: string;
    slug: string;
    category?: string;
    points: number;
    solved_at: number;
  }

  interface ProfileChallenge {
    name: string;
    slug: string;
    category: string;
    category_color: string;
    difficulty: string;
    points: number;
    awarded_points: number;
    solved_flags: number;
    total_flags: number;
    solved: boolean;
    solved_at?: number;
    blood_rank: number;
  }

  interface ChallengeGroup {
    name: string;
    color: string;
    solved: number;
    total: number;
    items: ProfileChallenge[];
  }

  const difficultyColors: Record<string, string> = {
    easy: "#6f9d78",
    medium: "#b89a55",
    hard: "#b76f68",
    insane: "#8e78ac",
  };

  $: username = $page.params.username ?? "";

  let loading = true;
  let error = "";
  let notFound = false;
  let totalScore = 0;
  let solvedCount = 0;
  let rank: number | null = null;
  let solves: Solve[] = [];
  let challenges: ProfileChallenge[] = [];

  let profileView: "challenges" | "analytics" = "challenges";
  let challengeSearch = "";
  let solvedOnly = false;
  let groupSort: "category" | "progress" = "category";
  let collapsed = new Set<string>();
  let desktop = false;
  let profileController: AbortController | null = null;
  $: searchActive = challengeSearch.trim().length > 0;

  const catOf = (solve: Solve) => solve.category || "Uncategorized";
  const catColor = (solve: Solve) => categoryColor(solve.category);

  $: sorted = [...solves].sort((a, b) => a.solved_at - b.solved_at);
  $: t0 = sorted.length ? sorted[0].solved_at : 0;
  $: lastAt = sorted.length ? sorted[sorted.length - 1].solved_at : 0;
  $: spanSec = lastAt - t0;

  $: rankLabel = rank != null ? `#${rank}` : "—";
  $: spanLabel = spanSec > 0 ? formatDur(spanSec) : "—";
  $: firstDate = t0 ? parseInstant(t0, "seconds") : null;
  $: firstDateLabel = formatLocalDate(firstDate);
  $: firstTimeLabel = formatLocalTimeWithZone(firstDate);

  $: scorePoints = (() => {
    let cumulative = 0;
    return sorted.map((solve) => {
      cumulative += solve.points;
      return {
        x: solve.solved_at - t0,
        y: cumulative,
        color: catColor(solve),
        label: solve.name,
      };
    });
  })();

  $: categories = (() => {
    const map = new Map<
      string,
      {
        name: string;
        color: string;
        total: number;
        segments: { points: number; label: string }[];
      }
    >();
    for (const solve of solves) {
      const key = catOf(solve);
      if (!map.has(key)) {
        map.set(key, {
          name: key,
          color: catColor(solve),
          total: 0,
          segments: [],
        });
      }
      const category = map.get(key)!;
      category.total += solve.points;
      category.segments.push({ points: solve.points, label: solve.name });
    }
    return [...map.values()].sort((a, b) => b.total - a.total);
  })();

  $: rowIndex = new Map(
    categories.map((category, index) => [category.name, index]),
  );
  $: timelineRows = categories.map((category) => ({
    name: category.name,
    color: category.color,
  }));
  $: timelineSolves = solves.map((solve) => ({
    row: rowIndex.get(catOf(solve)) ?? 0,
    x: solve.solved_at - t0,
    color: catColor(solve),
    label: solve.name,
  }));
  $: cadenceSolves = sorted.map((solve) => ({
    x: solve.solved_at - t0,
    color: catColor(solve),
    label: solve.name,
  }));

  $: difficultyRows = ["easy", "medium", "hard", "insane"]
    .map((difficulty) => {
      const entries = challenges.filter(
        (challenge) => challenge.difficulty === difficulty,
      );
      return {
        name: difficulty,
        solved: entries.filter((challenge) => challenge.solved).length,
        total: entries.length,
        color: difficultyColors[difficulty],
      };
    })
    .filter((row) => row.total > 0);

  $: allChallengeGroups = (() => {
    const map = new Map<string, ChallengeGroup>();
    for (const challenge of challenges) {
      const name = challenge.category || "Uncategorized";
      if (!map.has(name)) {
        map.set(name, {
          name,
          color: categoryColor(name),
          solved: 0,
          total: 0,
          items: [],
        });
      }
      const group = map.get(name)!;
      group.total += 1;
      if (challenge.solved) group.solved += 1;
      group.items.push(challenge);
    }
    return [...map.values()];
  })();

  $: challengeGroups = (() => {
    const query = challengeSearch.trim().toLowerCase();
    const groups = allChallengeGroups
      .map((group) => ({
        ...group,
        items: group.items.filter((challenge) => {
          if (solvedOnly && !challenge.solved) return false;
          if (!query) return true;
          return [
            challenge.name,
            challenge.slug,
            challenge.category,
            challenge.difficulty,
          ].some((value) => value.toLowerCase().includes(query));
        }),
      }))
      .filter((group) => group.items.length > 0);

    if (groupSort === "progress") {
      groups.sort(
        (a, b) =>
          b.solved / Math.max(1, b.total) - a.solved / Math.max(1, a.total) ||
          a.name.localeCompare(b.name),
      );
    } else {
      groups.sort((a, b) => a.name.localeCompare(b.name));
    }
    return groups;
  })();

  $: allVisibleCollapsed =
    !searchActive &&
    challengeGroups.length > 0 &&
    challengeGroups.every((group) => collapsed.has(group.name));
  $: displayedSolvedCount = challenges.length
    ? challenges.filter((challenge) => challenge.solved).length
    : solvedCount;

  function toggleCategory(name: string) {
    const next = new Set(collapsed);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    collapsed = next;
  }

  function toggleAllCategories() {
    if (searchActive) return;
    collapsed = allVisibleCollapsed
      ? new Set()
      : new Set(challengeGroups.map((group) => group.name));
  }

  function bloodClass(rankValue: number) {
    if (rankValue === 1) return "border-blood/30 bg-blood/10 text-blood";
    if (rankValue === 2)
      return "border-stone-500/40 bg-stone-500/10 text-stone-300";
    return "border-amber-700/40 bg-amber-700/10 text-amber-600";
  }

  function relativeSolveTime(challenge: ProfileChallenge) {
    if (!challenge.solved_at || !t0) return "";
    return `T+${formatDur(challenge.solved_at - t0)}`;
  }

  async function load(name: string) {
    profileController?.abort();
    const controller = new AbortController();
    profileController = controller;
    loading = true;
    error = "";
    notFound = false;
    try {
      const response = await fetch(
        `${API_BASE}/api/v1/profile/${encodeURIComponent(name)}`,
        {
          headers: authHeaders(),
          signal: controller.signal,
        },
      );
      if (response.status === 404) {
        notFound = true;
        error = "Player not found";
        return;
      }
      if (!response.ok)
        throw new Error(`Failed to load profile (${response.status})`);
      const data = await response.json();
      solves = data.solves ?? [];
      challenges = data.challenges ?? [];
      totalScore = data.user?.total_score ?? 0;
      solvedCount = data.user?.challenges_solved ?? solves.length;
      rank = data.user?.global_rank ?? null;
    } catch (caught) {
      if (controller.signal.aborted) return;
      error =
        caught instanceof Error ? caught.message : "Failed to load profile";
    } finally {
      if (profileController === controller) loading = false;
    }
  }

  function authHeaders(): Record<string, string> {
    if (typeof localStorage === "undefined") return {};
    const token = localStorage.getItem("accessToken");
    return token ? { Authorization: `Bearer ${token}` } : {};
  }

  let loaded = "";
  $: if (username && username !== loaded) {
    loaded = username;
    challengeSearch = "";
    collapsed = new Set();
    load(username);
  }

  onMount(() => {
    const media = window.matchMedia("(min-width: 1024px)");
    const syncDesktop = () => (desktop = media.matches);
    syncDesktop();
    media.addEventListener("change", syncDesktop);
    if (username && username !== loaded) {
      loaded = username;
      load(username);
    }
    return () => media.removeEventListener("change", syncDesktop);
  });

  onDestroy(() => profileController?.abort());
</script>

<svelte:head>
  <title>{username} - Anvil</title>
</svelte:head>

<div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
  <a
    href="/scoreboard"
    class="inline-flex items-center gap-1.5 text-sm text-stone-500 hover:text-stone-300 transition mb-6"
  >
    <OpticalIcon icon="mdi:arrow-left" size={14} box={14} />
    <span class="optical-label leading-none">Scoreboard</span>
  </a>

  {#if loading}
    <div class="flex items-center justify-center py-20">
      <Icon icon="mdi:loading" class="w-6 h-6 text-stone-500 animate-spin" />
    </div>
  {:else if error}
    <EmptyState icon="mdi:account-question" text={error}>
      {#if notFound}
        <p class="mt-1 text-sm text-stone-600">
          No player named <span class="text-stone-400">{username}</span>.
        </p>
      {:else}
        <button
          on:click={() => load(username)}
          class="mt-3 text-xs text-amber-500 hover:text-amber-400">Retry</button
        >
      {/if}
    </EmptyState>
  {:else}
    <div
      class="mb-6 flex items-end justify-between gap-4 border-b border-stone-800 pb-5"
    >
      <div class="min-w-0">
        <p class="metadata-label mb-1.5 text-stone-600">Player profile</p>
        <h1
          class="truncate text-2xl font-semibold tracking-tight text-stone-100"
        >
          {username}
        </h1>
      </div>
      <div class="text-right">
        <p class="text-2xl font-semibold tabular-nums text-amber-500">
          {rankLabel}
        </p>
        <p class="metadata-label mt-1 text-stone-600">Global rank</p>
      </div>
    </div>

    <div class="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-3 mb-6">
      <StatTile label="Points" value={totalScore.toLocaleString()} accent />
      <StatTile
        label="Solved"
        value={displayedSolvedCount}
        sub={challenges.length ? `of ${challenges.length}` : ""}
      />
      <StatTile label="Solve events" value={solves.length} />
      <StatTile
        label="First solve"
        value={firstDateLabel}
        sub={firstTimeLabel}
        title={instantTitle(firstDate)}
      />
      <div class="col-span-2 sm:col-span-2 lg:col-span-1">
        <StatTile label="Active span" value={spanLabel} sub="first → last" />
      </div>
    </div>

    <div
      class="mb-4 grid grid-cols-2 rounded-md border border-stone-800 p-1 lg:hidden"
    >
      <button
        class="rounded px-3 py-2 text-sm transition-colors {profileView ===
        'challenges'
          ? 'bg-stone-800 text-stone-100'
          : 'text-stone-500'}"
        on:click={() => (profileView = "challenges")}
        aria-pressed={profileView === "challenges"}
      >
        Challenges
      </button>
      <button
        class="rounded px-3 py-2 text-sm transition-colors {profileView ===
        'analytics'
          ? 'bg-stone-800 text-stone-100'
          : 'text-stone-500'}"
        on:click={() => (profileView = "analytics")}
        aria-pressed={profileView === "analytics"}
      >
        Analytics
      </button>
    </div>

    <div
      class="grid items-start gap-6 lg:grid-cols-[minmax(320px,5fr)_minmax(0,7fr)] lg:items-stretch"
    >
      {#if desktop || profileView === "challenges"}
        <section class="lg:relative lg:min-h-0">
          <Card
            title="Challenge progress"
            bodyClass="flex min-h-0 flex-1 flex-col p-0"
            className="lg:absolute lg:inset-0 lg:flex lg:flex-col"
          >
            <span slot="meta" class="text-xs tabular-nums text-stone-500">
              {displayedSolvedCount}/{challenges.length}
            </span>

            <div class="space-y-3 border-b border-stone-800 p-4">
              <div class="relative">
                <Icon
                  icon="mdi:magnify"
                  class="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-stone-600"
                />
                <input
                  bind:value={challengeSearch}
                  type="search"
                  placeholder="Search challenges"
                  aria-label="Search profile challenges"
                  class="w-full rounded-md border border-stone-800 bg-stone-950 py-2 pl-9 pr-3 text-sm text-stone-200 placeholder-stone-600 focus:border-stone-600 focus:outline-none"
                />
              </div>
              <div class="flex flex-wrap items-center gap-2">
                <label
                  class="inline-flex cursor-pointer select-none items-center gap-2 rounded-md border border-stone-800 px-2.5 py-1.5"
                >
                  <input
                    type="checkbox"
                    bind:checked={solvedOnly}
                    class="h-3.5 w-3.5 rounded-sm border-stone-700 bg-stone-950 accent-amber-600"
                  />
                  <span class="optical-label metadata-label text-stone-500"
                    >Solved only</span
                  >
                </label>
                <button
                  on:click={toggleAllCategories}
                  disabled={searchActive}
                  class="inline-flex items-center gap-1.5 rounded-md border border-stone-800 px-2.5 py-1.5 text-xs leading-none text-stone-500 hover:text-stone-300"
                  aria-label={searchActive
                    ? "Clear search to collapse categories"
                    : allVisibleCollapsed
                      ? "Expand all categories"
                      : "Collapse all categories"}
                  title={searchActive
                    ? "Clear search to change collapsed categories"
                    : ""}
                >
                  <OpticalIcon
                    icon={allVisibleCollapsed
                      ? "mdi:unfold-more-horizontal"
                      : "mdi:unfold-less-horizontal"}
                    size={12}
                    box={12}
                  />
                  <span class="optical-label"
                    >{allVisibleCollapsed ? "Expand all" : "Collapse all"}</span
                  >
                </button>
                <button
                  on:click={() =>
                    (groupSort =
                      groupSort === "category" ? "progress" : "category")}
                  class="ml-auto inline-flex items-center gap-1.5 rounded-md border border-stone-800 px-2.5 py-1.5 text-xs leading-none text-stone-500 hover:text-stone-300"
                  title="Change category ordering"
                >
                  <OpticalIcon icon="mdi:sort" size={12} box={12} />
                  <span class="optical-label"
                    >{groupSort === "category" ? "Category" : "Progress"}</span
                  >
                </button>
              </div>
            </div>

            {#if challengeGroups.length === 0}
              <div class="px-4 py-10 text-center text-sm text-stone-500">
                No challenges match the current filters.
              </div>
            {:else}
              <div class="lg:min-h-0 lg:flex-1 lg:overflow-y-auto">
                {#each challengeGroups as group (group.name)}
                  <div class="border-b border-stone-800/70 last:border-b-0">
                    <button
                      on:click={() => toggleCategory(group.name)}
                      class="flex w-full items-center gap-2.5 px-4 py-3 text-left hover:bg-stone-800/20"
                      aria-expanded={searchActive || !collapsed.has(group.name)}
                    >
                      <span
                        class="h-2 w-2 shrink-0 rounded-full"
                        style="background: {group.color};"
                      ></span>
                      <span
                        class="optical-label truncate text-sm font-medium text-stone-200"
                        >{group.name}</span
                      >
                      <span
                        class="optical-label ml-auto text-xs tabular-nums text-stone-500"
                        >{group.solved}/{group.total}</span
                      >
                      <OpticalIcon
                        icon={!searchActive && collapsed.has(group.name)
                          ? "mdi:chevron-right"
                          : "mdi:chevron-down"}
                        size={14}
                        box={14}
                        className="text-stone-600"
                      />
                    </button>

                    {#if searchActive || !collapsed.has(group.name)}
                      <div class="border-t border-stone-800/50 bg-stone-950/20">
                        {#each group.items as challenge (challenge.slug)}
                          <a
                            href="/challenges/{challenge.slug}"
                            class="group flex min-h-12 items-center gap-2.5 border-b border-stone-800/40 px-4 py-2.5 last:border-b-0 hover:bg-stone-800/20"
                          >
                            <span
                              class="flex h-5 w-5 shrink-0 items-center justify-center rounded border {challenge.solved
                                ? 'border-up/25 bg-up/10 text-up'
                                : 'border-stone-800 text-stone-700'}"
                              role="img"
                              aria-label={challenge.solved
                                ? "Solved"
                                : challenge.solved_flags > 0
                                  ? `${challenge.solved_flags} of ${challenge.total_flags} flags solved`
                                  : "Unsolved"}
                            >
                              {#if challenge.solved}
                                <Icon
                                  icon="mdi:check"
                                  class="h-3 w-3"
                                  aria-hidden="true"
                                />
                              {:else if challenge.solved_flags > 0}
                                <span
                                  aria-hidden="true"
                                  class="text-[0.6rem] tabular-nums"
                                  >{challenge.solved_flags}</span
                                >
                              {/if}
                            </span>
                            <span class="min-w-0 flex-1">
                              <span
                                class="block truncate text-sm {challenge.solved
                                  ? 'text-stone-200'
                                  : 'text-stone-400'}">{challenge.name}</span
                              >
                              <span
                                class="metadata-label mt-1 block capitalize text-stone-600"
                                >{challenge.difficulty}</span
                              >
                            </span>
                            {#if challenge.blood_rank > 0 && challenge.blood_rank <= 3}
                              <span
                                class="badge-label rounded-full border px-1.5 py-1 text-[0.62rem] font-semibold tabular-nums {bloodClass(
                                  challenge.blood_rank,
                                )}"
                                title="Blood placement #{challenge.blood_rank}"
                                role="img"
                                aria-label="Blood placement {challenge.blood_rank}"
                              >
                                <span aria-hidden="true"
                                  >{challenge.blood_rank}</span
                                >
                              </span>
                            {/if}
                            <span class="shrink-0 text-right">
                              {#if challenge.solved}
                                <span
                                  class="block text-xs tabular-nums text-stone-500"
                                  >{relativeSolveTime(challenge)}</span
                                >
                              {/if}
                              <span
                                class="block text-xs tabular-nums {challenge.solved
                                  ? 'text-stone-300'
                                  : 'text-stone-600'}"
                              >
                                {challenge.solved
                                  ? challenge.awarded_points
                                  : challenge.points} pts
                              </span>
                            </span>
                          </a>
                        {/each}
                      </div>
                    {/if}
                  </div>
                {/each}
              </div>
            {/if}
          </Card>
        </section>
      {/if}

      {#if desktop || profileView === "analytics"}
        <section class="space-y-6">
          {#if solves.length === 0}
            <Card title="Analytics">
              <EmptyState icon="mdi:chart-line" text="No solve data yet." />
            </Card>
          {:else}
            <Card title="Solve points over time">
              <span slot="meta" class="metadata-label text-stone-500"
                >Cumulative</span
              >
              <ProfileScoreChart points={scorePoints} height={240} />
            </Card>

            <Card title="Points by category">
              <span slot="meta" class="metadata-label text-stone-500"
                >{categories.length} categories</span
              >
              <CategoryBars {categories} />
            </Card>

            <Card title="Solve timeline">
              <span
                slot="meta"
                class="inline-flex items-center gap-1 leading-none text-stone-500"
              >
                <span class="optical-label metadata-label">First</span>
                <OpticalIcon icon="mdi:arrow-right" size={11} box={11} />
                <span class="optical-label metadata-label">latest</span>
              </span>
              <SolveTimeline rows={timelineRows} solves={timelineSolves} />
            </Card>

            <div class="grid gap-6 md:grid-cols-2">
              <Card title="Solve cadence">
                <SolveCadence solves={cadenceSolves} />
              </Card>
              <Card title="Difficulty progress">
                <DifficultyBars rows={difficultyRows} />
              </Card>
            </div>
          {/if}
        </section>
      {/if}
    </div>
  {/if}
</div>
