<script lang="ts">
  import Icon from "@iconify/svelte";
  import { onDestroy } from "svelte";
  import { page } from "$app/stores";
  import { API_BASE } from "$lib/config";
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
  import DifficultyBars from "$lib/components/DifficultyBars.svelte";
  import OpticalIcon from "$lib/components/OpticalIcon.svelte";

  interface TeamMember {
    username: string;
    display_name?: string;
  }

  interface Team {
    id: string;
    name: string;
    total_score: number;
    challenges_solved: number;
    global_rank: number;
    last_solve_at?: number;
    members: TeamMember[];
  }

  interface Solve {
    name: string;
    slug: string;
    category?: string;
    category_color?: string;
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

  $: teamID = $page.params.id ?? "";

  let loading = true;
  let error = "";
  let notFound = false;
  let team: Team | null = null;
  let economy = false;
  let solves: Solve[] = [];
  let challenges: ProfileChallenge[] = [];
  let challengeSearch = "";
  let solvedOnly = false;
  let collapsed = new Set<string>();
  let requestController: AbortController | null = null;

  const memberName = (member: TeamMember) =>
    member.display_name || member.username;
  const categoryName = (solve: Solve) => solve.category || "Uncategorized";
  const categoryColor = (solve: Solve) => solve.category_color || "#94a3b8";

  $: sortedSolves = [...solves].sort((a, b) => a.solved_at - b.solved_at);
  $: firstAt = sortedSolves.length ? sortedSolves[0].solved_at : 0;
  $: lastAt = sortedSolves.length
    ? sortedSolves[sortedSolves.length - 1].solved_at
    : 0;
  $: activeSpan =
    firstAt && lastAt > firstAt ? formatDur(lastAt - firstAt) : "-";
  $: firstDate = firstAt ? parseInstant(firstAt, "seconds") : null;
  $: firstDateLabel = formatLocalDate(firstDate);
  $: firstTimeLabel = formatLocalTimeWithZone(firstDate);

  $: scorePoints = (() => {
    let cumulative = 0;
    return sortedSolves.map((solve) => {
      cumulative += solve.points;
      return {
        x: solve.solved_at - firstAt,
        y: cumulative,
        color: categoryColor(solve),
        label: solve.name,
      };
    });
  })();

  $: categories = (() => {
    const grouped = new Map<
      string,
      {
        name: string;
        color: string;
        total: number;
        segments: { points: number; label: string }[];
      }
    >();
    for (const solve of solves) {
      const name = categoryName(solve);
      if (!grouped.has(name)) {
        grouped.set(name, {
          name,
          color: categoryColor(solve),
          total: 0,
          segments: [],
        });
      }
      const category = grouped.get(name)!;
      category.total += solve.points;
      category.segments.push({ points: solve.points, label: solve.name });
    }
    return [...grouped.values()].sort((a, b) => b.total - a.total);
  })();

  $: difficultyRows = ["easy", "medium", "hard", "insane"]
    .map((difficulty) => {
      const matches = challenges.filter(
        (challenge) => challenge.difficulty === difficulty,
      );
      return {
        name: difficulty,
        solved: matches.filter((challenge) => challenge.solved).length,
        total: matches.length,
        color: difficultyColors[difficulty],
      };
    })
    .filter((row) => row.total > 0);

  $: challengeGroups = (() => {
    const grouped = new Map<string, ChallengeGroup>();
    const query = challengeSearch.trim().toLowerCase();
    for (const challenge of challenges) {
      if (solvedOnly && !challenge.solved) continue;
      if (
        query &&
        ![
          challenge.name,
          challenge.slug,
          challenge.category,
          challenge.difficulty,
        ].some((value) => value.toLowerCase().includes(query))
      )
        continue;
      const name = challenge.category || "Uncategorized";
      if (!grouped.has(name)) {
        grouped.set(name, {
          name,
          color: challenge.category_color || "#94a3b8",
          solved: 0,
          total: 0,
          items: [],
        });
      }
      const group = grouped.get(name)!;
      group.total += 1;
      if (challenge.solved) group.solved += 1;
      group.items.push(challenge);
    }
    return [...grouped.values()].sort((a, b) => a.name.localeCompare(b.name));
  })();

  function authHeaders(): Record<string, string> {
    if (typeof localStorage === "undefined") return {};
    const token = localStorage.getItem("accessToken");
    return token ? { Authorization: `Bearer ${token}` } : {};
  }

  async function load(id: string) {
    requestController?.abort();
    const controller = new AbortController();
    requestController = controller;
    loading = true;
    error = "";
    notFound = false;
    team = null;
    try {
      const response = await fetch(
        `${API_BASE}/api/v1/teams/${encodeURIComponent(id)}`,
        { headers: authHeaders(), signal: controller.signal },
      );
      if (response.status === 404) {
        notFound = true;
        error = "Team not found";
        return;
      }
      if (!response.ok)
        throw new Error(`Failed to load team (${response.status})`);
      const data = await response.json();
      team = data.team ?? null;
      economy = !!data.economy;
      solves = data.solves ?? [];
      challenges = data.challenges ?? [];
    } catch (caught) {
      if (controller.signal.aborted) return;
      error = caught instanceof Error ? caught.message : "Failed to load team";
    } finally {
      if (requestController === controller) loading = false;
    }
  }

  function toggleGroup(name: string) {
    const next = new Set(collapsed);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    collapsed = next;
  }

  function bloodClass(rank: number) {
    if (rank === 1) return "border-blood/30 bg-blood/10 text-blood";
    if (rank === 2) return "border-stone-500/40 bg-stone-500/10 text-stone-300";
    return "border-amber-700/40 bg-amber-700/10 text-amber-600";
  }

  function relativeSolveTime(challenge: ProfileChallenge) {
    if (!challenge.solved_at || !firstAt) return "";
    return `T+${formatDur(challenge.solved_at - firstAt)}`;
  }

  let loaded = "";
  $: if (teamID && teamID !== loaded) {
    loaded = teamID;
    challengeSearch = "";
    collapsed = new Set();
    void load(teamID);
  }

  onDestroy(() => requestController?.abort());
</script>

<svelte:head>
  <title>{team?.name ?? "Team"} - Anvil</title>
</svelte:head>

<div class="w-full px-4 py-8 sm:px-6 lg:px-8 2xl:px-10">
  <a
    href="/scoreboard"
    class="mb-6 inline-flex items-center gap-1.5 text-sm text-stone-500 transition hover:text-stone-300"
  >
    <OpticalIcon icon="mdi:arrow-left" size={14} box={14} />
    <span class="optical-label leading-none">Scoreboard</span>
  </a>

  {#if loading}
    <div class="flex items-center justify-center py-20">
      <Icon icon="mdi:loading" class="h-6 w-6 animate-spin text-stone-500" />
    </div>
  {:else if error || !team}
    <EmptyState
      icon="mdi:account-group-outline"
      text={error || "Team not found"}
    >
      {#if notFound}
        <p class="mt-1 text-sm text-stone-600">
          This team is not listed on the public scoreboard.
        </p>
      {:else}
        <button
          on:click={() => load(teamID)}
          class="mt-3 text-xs text-amber-500 hover:text-amber-400">Retry</button
        >
      {/if}
    </EmptyState>
  {:else}
    <div
      class="mb-6 flex items-end justify-between gap-4 border-b border-stone-800 pb-5"
    >
      <div class="min-w-0">
        <p class="metadata-label mb-1.5 text-stone-600">Team profile</p>
        <h1
          class="truncate text-2xl font-semibold tracking-tight text-stone-100"
        >
          {team.name}
        </h1>
      </div>
      <div class="text-right">
        <p class="text-2xl font-semibold tabular-nums text-amber-500">
          #{team.global_rank}
        </p>
        <p class="metadata-label mt-1 text-stone-600">Global rank</p>
      </div>
    </div>

    <div class="mb-6 grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
      <StatTile
        label={economy ? "Ledger score" : "Points"}
        value={team.total_score.toLocaleString()}
        accent
      />
      <StatTile
        label="Solved"
        value={team.challenges_solved}
        sub={challenges.length ? `of ${challenges.length}` : ""}
      />
      <StatTile label="Members" value={team.members.length} />
      <StatTile
        label="First solve"
        value={firstDateLabel}
        sub={firstTimeLabel}
        title={instantTitle(firstDate)}
      />
      <div class="col-span-2 sm:col-span-2 lg:col-span-1">
        <StatTile
          label="Active span"
          value={activeSpan}
          sub="first to latest"
        />
      </div>
    </div>

    <div
      class="grid items-start gap-6 xl:grid-cols-[minmax(360px,5fr)_minmax(0,7fr)]"
    >
      <section>
        <Card title="Challenge progress" bodyClass="p-0">
          <span slot="meta" class="text-xs tabular-nums text-stone-500">
            {team.challenges_solved}/{challenges.length}
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
                aria-label="Search team challenges"
                class="w-full rounded-md border border-stone-800 bg-stone-950 py-2 pl-9 pr-3 text-sm text-stone-200 placeholder-stone-600 focus:border-stone-600 focus:outline-none"
              />
            </div>
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
          </div>

          {#if challengeGroups.length === 0}
            <div class="px-4 py-10 text-center text-sm text-stone-500">
              No challenges match the current filters.
            </div>
          {:else}
            <div class="max-h-[54rem] overflow-y-auto">
              {#each challengeGroups as group (group.name)}
                <div class="border-b border-stone-800/70 last:border-b-0">
                  <button
                    on:click={() => toggleGroup(group.name)}
                    class="flex w-full items-center gap-2.5 px-4 py-3 text-left hover:bg-stone-800/20"
                    aria-expanded={!collapsed.has(group.name)}
                  >
                    <span
                      class="h-2 w-2 shrink-0 rounded-full"
                      style="background: {group.color};"
                    ></span>
                    <span
                      class="optical-label truncate text-sm font-medium text-stone-200"
                    >
                      {group.name}
                    </span>
                    <span
                      class="optical-label ml-auto text-xs tabular-nums text-stone-500"
                    >
                      {group.solved}/{group.total}
                    </span>
                    <OpticalIcon
                      icon={collapsed.has(group.name)
                        ? "mdi:chevron-right"
                        : "mdi:chevron-down"}
                      size={14}
                      box={14}
                      className="text-stone-600"
                    />
                  </button>

                  {#if !collapsed.has(group.name)}
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
                              >
                                {challenge.solved_flags}
                              </span>
                            {/if}
                          </span>
                          <span class="min-w-0 flex-1">
                            <span class="block truncate text-sm text-stone-200">
                              {challenge.name}
                            </span>
                            <span
                              class="metadata-label mt-1 block capitalize text-stone-600"
                            >
                              {challenge.difficulty}
                            </span>
                          </span>
                          {#if challenge.blood_rank > 0 && challenge.blood_rank <= 3}
                            <span
                              class="badge-label rounded-full border px-1.5 py-1 text-[0.62rem] font-semibold tabular-nums {bloodClass(
                                challenge.blood_rank,
                              )}"
                              title="Blood placement #{challenge.blood_rank}"
                            >
                              {challenge.blood_rank}
                            </span>
                          {/if}
                          <span class="shrink-0 text-right">
                            {#if challenge.solved}
                              <span
                                class="block text-xs tabular-nums text-stone-500"
                              >
                                {relativeSolveTime(challenge)}
                              </span>
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

      <section class="space-y-6">
        <Card title="Roster" bodyClass="p-0">
          <span slot="meta" class="metadata-label text-stone-500">
            {team.members.length}
            {team.members.length === 1 ? "member" : "members"}
          </span>
          {#if team.members.length === 0}
            <div class="p-4 text-sm text-stone-500">No active members.</div>
          {:else}
            <div class="grid sm:grid-cols-2">
              {#each team.members as member, index (member.username)}
                <a
                  href="/profile/{encodeURIComponent(member.username)}"
                  class="flex min-w-0 items-center gap-3 border-stone-800/70 px-4 py-3 hover:bg-stone-800/20 {index >
                  0
                    ? 'border-t'
                    : ''} {index === 1 ? 'sm:border-t-0' : ''} {index % 2 === 1
                    ? 'sm:border-l'
                    : ''}"
                >
                  <span
                    class="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-stone-800 text-xs font-semibold uppercase text-stone-400"
                  >
                    {memberName(member).slice(0, 2)}
                  </span>
                  <span class="min-w-0">
                    <span class="block truncate text-sm text-stone-200"
                      >{memberName(member)}</span
                    >
                    {#if member.display_name}
                      <span class="block truncate text-xs text-stone-600"
                        >@{member.username}</span
                      >
                    {/if}
                  </span>
                  <OpticalIcon
                    icon="mdi:chevron-right"
                    size={14}
                    box={14}
                    className="ml-auto text-stone-700"
                  />
                </a>
              {/each}
            </div>
          {/if}
        </Card>

        {#if solves.length === 0}
          <Card title="Analytics">
            <EmptyState icon="mdi:chart-line" text="No solve data yet." />
          </Card>
        {:else}
          <Card
            title={economy
              ? "Held challenge value over time"
              : "Solve points over time"}
          >
            <span slot="meta" class="metadata-label text-stone-500"
              >Cumulative</span
            >
            <ProfileScoreChart points={scorePoints} height={240} />
          </Card>

          <div class="grid gap-6 md:grid-cols-2 xl:grid-cols-1 2xl:grid-cols-2">
            <Card title="Points by category">
              <CategoryBars {categories} />
            </Card>
            <Card title="Difficulty progress">
              <DifficultyBars rows={difficultyRows} />
            </Card>
          </div>
        {/if}
      </section>
    </div>
  {/if}
</div>
