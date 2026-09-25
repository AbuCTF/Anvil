<script lang="ts">
  import { onMount } from "svelte";
  import Icon from "@iconify/svelte";
  import { api } from "$api";
  import { categoryColor } from "$lib/rank";
  import Card from "$lib/components/Card.svelte";
  import StatTile from "$lib/components/StatTile.svelte";
  import EmptyState from "$lib/components/EmptyState.svelte";
  import ProfileScoreChart from "$lib/components/ProfileScoreChart.svelte";
  import CategoryBars from "$lib/components/CategoryBars.svelte";
  import DifficultyBars from "$lib/components/DifficultyBars.svelte";
  import OpticalIcon from "$lib/components/OpticalIcon.svelte";
  import {
    formatLocalDateLong,
    formatLocalTime,
    formatLocalTimeWithZone,
    instantTitle,
    viewerTimeZone as getViewerTimeZone,
  } from "$lib/time";

  interface Profile {
    username: string;
    display_name?: string | null;
    email?: string | null;
    bio?: string | null;
    role: string;
    joined_at: number;
  }

  interface UserStats {
    total_score: number;
    total_solves: number;
    total_challenges_solved: number;
    total_attempts: number;
    hints_unlocked: number;
    points_spent_on_hints: number;
    rank: number;
    solves_by_difficulty: Record<string, number>;
    solves_by_category: Record<string, number>;
  }

  interface Progress {
    solved: number;
    total: number;
  }

  interface Solve {
    challenge_name: string;
    challenge_slug: string;
    flag_name: string;
    points: number;
    solved_at: number;
  }

  interface Challenge {
    name: string;
    slug: string;
    difficulty?: string;
    category?: string;
    total_flags?: number;
    user_solves?: number;
  }

  const difficultyColors: Record<string, string> = {
    easy: "#6f9d78",
    medium: "#b89a55",
    hard: "#b76f68",
    insane: "#8e78ac",
  };
  const viewerTimeZone = getViewerTimeZone();
  const viewerTimeZoneLabel = viewerTimeZone.replaceAll("_", " ");

  let profile: Profile | null = null;
  let stats: UserStats | null = null;
  let solves: Solve[] = [];
  let challenges: Challenge[] = [];
  let byDifficulty: Record<string, Progress> = {};
  let byCategory: Record<string, Progress> = {};
  let loading = true;
  let error = "";
  let analyticsLoadError = "";
  let challengeLoadError = "";
  let editing = false;
  let editForm = { display_name: "", bio: "" };
  let saving = false;
  let saveError = "";

  $: displayName = profile?.display_name || profile?.username || "";
  $: challengeBySlug = new Map(
    challenges.map((challenge) => [challenge.slug, challenge]),
  );
  $: completedChallenges = challenges.length
    ? challenges.filter((challenge) => {
        const total = challenge.total_flags ?? 0;
        return total > 0 && (challenge.user_solves ?? 0) >= total;
      }).length
    : (stats?.total_challenges_solved ?? 0);
  $: sortedSolves = [...solves].sort((a, b) => a.solved_at - b.solved_at);
  $: recentSolves = [...sortedSolves].reverse().slice(0, 10);
  $: firstSolve = sortedSolves[0]?.solved_at ?? 0;
  $: firstSolveDate = firstSolve ? formatLocalDateLong(firstSolve) : "-";
  $: firstSolveTime = firstSolve ? formatLocalTimeWithZone(firstSolve) : "";
  $: joinedDate = profile?.joined_at
    ? formatLocalDateLong(profile.joined_at)
    : "-";

  $: difficultyRows = Object.entries(byDifficulty)
    .map(([name, data]) => ({
      name,
      solved: data.solved,
      total: data.total,
      color: difficultyColors[name.toLowerCase()] ?? "#78716c",
    }))
    .sort((a, b) => {
      const order = ["easy", "medium", "hard", "insane"];
      const ai = order.indexOf(a.name.toLowerCase());
      const bi = order.indexOf(b.name.toLowerCase());
      return (ai < 0 ? order.length : ai) - (bi < 0 ? order.length : bi);
    });

  $: categoryRows = Object.entries(byCategory)
    .map(([name, data]) => ({ name, ...data, color: categoryColor(name) }))
    .sort((a, b) => b.solved - a.solved || a.name.localeCompare(b.name));

  $: scorePoints = (() => {
    let cumulative = 0;
    const start = sortedSolves[0]?.solved_at ?? 0;
    return sortedSolves.map((solve) => {
      cumulative += solve.points;
      const category =
        challengeBySlug.get(solve.challenge_slug)?.category || "Uncategorized";
      return {
        x: solve.solved_at - start,
        y: cumulative,
        color: categoryColor(category),
        label: solve.challenge_name,
      };
    });
  })();

  $: categoryPoints = (() => {
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
      const category =
        challengeBySlug.get(solve.challenge_slug)?.category || "Uncategorized";
      if (!grouped.has(category)) {
        grouped.set(category, {
          name: category,
          color: categoryColor(category),
          total: 0,
          segments: [],
        });
      }
      const row = grouped.get(category)!;
      row.total += solve.points;
      row.segments.push({ points: solve.points, label: solve.challenge_name });
    }
    return [...grouped.values()].sort((a, b) => b.total - a.total);
  })();

  onMount(loadProfile);

  async function loadProfile() {
    loading = true;
    error = "";
    analyticsLoadError = "";
    challengeLoadError = "";
    try {
      const [profileState, statsState, solvesState, challengeState] =
        await Promise.allSettled([
          api.getProfile(),
          api.getUserStats(),
          api.getUserSolves(),
          api.getChallenges(),
        ]);
      if (profileState.status === "rejected") throw profileState.reason;

      profile = profileState.value;
      stats =
        statsState.status === "fulfilled"
          ? statsState.value
          : {
              total_score: 0,
              total_solves: 0,
              total_challenges_solved: 0,
              total_attempts: 0,
              hints_unlocked: 0,
              points_spent_on_hints: 0,
              rank: 0,
              solves_by_difficulty: {},
              solves_by_category: {},
            };
      solves =
        solvesState.status === "fulfilled"
          ? solvesState.value.solves || []
          : [];
      challenges =
        challengeState.status === "fulfilled"
          ? challengeState.value.challenges || []
          : [];
      if (
        statsState.status === "rejected" ||
        solvesState.status === "rejected"
      ) {
        analyticsLoadError =
          "Some score analytics are temporarily unavailable. Profile editing remains available.";
      }
      if (challengeState.status === "rejected") {
        challengeLoadError = "Challenge progress is temporarily unavailable.";
      }

      const difficultyProgress: Record<string, Progress> = {};
      const categoryProgress: Record<string, Progress> = {};
      for (const challenge of challenges) {
        const difficulty = challenge.difficulty || "Unrated";
        const category = challenge.category || "Uncategorized";
        const complete =
          (challenge.total_flags ?? 0) > 0 &&
          (challenge.user_solves ?? 0) >= (challenge.total_flags ?? 0);
        difficultyProgress[difficulty] ??= { solved: 0, total: 0 };
        categoryProgress[category] ??= { solved: 0, total: 0 };
        difficultyProgress[difficulty].total += 1;
        categoryProgress[category].total += 1;
        if (complete) {
          difficultyProgress[difficulty].solved += 1;
          categoryProgress[category].solved += 1;
        }
      }
      byDifficulty = difficultyProgress;
      byCategory = categoryProgress;
      resetEditForm();
    } catch (caught) {
      error =
        caught instanceof Error ? caught.message : "Failed to load profile";
    } finally {
      loading = false;
    }
  }

  async function saveProfile() {
    if (saving) return;
    saving = true;
    saveError = "";
    try {
      const saved = {
        display_name: editForm.display_name.trim(),
        bio: editForm.bio.trim(),
      };
      await api.updateProfile(saved);
      profile = profile ? { ...profile, ...saved } : profile;
      editForm = saved;
      editing = false;
    } catch (caught) {
      saveError =
        caught instanceof Error ? caught.message : "Failed to save profile";
    } finally {
      saving = false;
    }
  }

  function resetEditForm() {
    editForm = {
      display_name: profile?.display_name || "",
      bio: profile?.bio || "",
    };
  }

  function beginEditing() {
    resetEditForm();
    saveError = "";
    editing = true;
  }

  function cancelEditing() {
    resetEditForm();
    saveError = "";
    editing = false;
  }
</script>

<svelte:head>
  <title>Profile - Anvil</title>
</svelte:head>

<div class="w-full px-4 py-8 sm:px-6 lg:px-8 2xl:px-10">
  {#if loading}
    <div
      class="flex min-h-[60vh] items-center justify-center"
      aria-label="Loading profile"
    >
      <Icon icon="mdi:loading" class="h-6 w-6 animate-spin text-stone-500" />
    </div>
  {:else if error}
    <Card title="Profile unavailable" className="mx-auto max-w-2xl">
      <EmptyState icon="mdi:account-alert-outline" text={error}>
        <button
          on:click={loadProfile}
          class="mt-4 inline-flex items-center gap-1.5 rounded-md border border-stone-700 px-3 py-2 text-sm leading-none text-stone-300 transition-colors hover:border-stone-600 hover:text-stone-100"
        >
          <OpticalIcon icon="mdi:refresh" size={14} box={14} />
          <span class="optical-label">Try again</span>
        </button>
      </EmptyState>
    </Card>
  {:else if profile && stats}
    <div
      class="mb-6 flex flex-col gap-4 border-b border-stone-800 pb-5 sm:flex-row sm:items-end sm:justify-between"
    >
      <div class="min-w-0">
        <p class="metadata-label mb-1.5 text-stone-600">Your profile</p>
        <h1
          class="truncate text-2xl font-semibold tracking-tight text-stone-100"
        >
          {displayName}
        </h1>
        <p class="mt-1 text-sm text-stone-500">@{profile.username}</p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        {#if profile.role !== "admin"}
          <a
            href="/profile/{profile.username}"
            class="inline-flex items-center gap-1.5 rounded-md border border-stone-800 px-3 py-2 text-sm leading-none text-stone-400 transition-colors hover:border-stone-700 hover:text-stone-200"
          >
            <OpticalIcon icon="mdi:open-in-new" size={13} box={14} />
            <span class="optical-label">Public view</span>
          </a>
        {/if}
        {#if !editing}
          <button
            on:click={beginEditing}
            class="inline-flex items-center gap-1.5 rounded-md border border-stone-700 bg-stone-900/40 px-3 py-2 text-sm leading-none text-stone-200 transition-colors hover:border-stone-600 hover:bg-stone-800/50"
          >
            <OpticalIcon icon="mdi:pencil-outline" size={13} box={14} />
            <span class="optical-label">Edit profile</span>
          </button>
        {/if}
      </div>
    </div>

    <div class="mb-6 grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
      <StatTile
        label="Points"
        value={stats.total_score.toLocaleString()}
        accent
      />
      <StatTile
        label="Global rank"
        value={stats.rank ? `#${stats.rank}` : "-"}
      />
      <StatTile
        label="Completed"
        value={completedChallenges}
        sub={challenges.length ? `of ${challenges.length}` : ""}
      />
      <StatTile
        label="Solve events"
        value={stats.total_solves || solves.length}
      />
      <div class="col-span-2 sm:col-span-2 lg:col-span-1">
        <StatTile
          label="First solve"
          value={firstSolveDate}
          sub={firstSolveTime}
          title={firstSolve ? instantTitle(firstSolve) : ""}
        />
      </div>
    </div>

    {#if analyticsLoadError}
      <div
        role="status"
        class="mb-3 rounded-md border border-warn/25 bg-warn/5 px-3 py-2.5 text-sm text-warn"
      >
        {analyticsLoadError}
      </div>
    {/if}

    {#if challengeLoadError}
      <div
        role="status"
        class="mb-6 rounded-md border border-warn/25 bg-warn/5 px-3 py-2.5 text-sm text-warn"
      >
        {challengeLoadError} Identity, scores, and captures are still current.
      </div>
    {/if}

    <div
      class="grid items-start gap-6 lg:grid-cols-[minmax(280px,4fr)_minmax(0,8fr)]"
    >
      <section class="space-y-6">
        <Card title={editing ? "Edit profile" : "Profile details"}>
          {#if editing}
            <form on:submit|preventDefault={saveProfile} class="space-y-5">
              <div>
                <label
                  for="display_name"
                  class="metadata-label mb-2 block text-stone-500"
                  >Display name</label
                >
                <input
                  id="display_name"
                  type="text"
                  bind:value={editForm.display_name}
                  maxlength="100"
                  class="w-full rounded-md border border-stone-800 bg-stone-950 px-3 py-2.5 text-sm text-stone-100 placeholder-stone-600 transition-colors focus:border-stone-600 focus:outline-none"
                  placeholder="Your display name"
                />
                <p
                  class="mt-1.5 text-right text-xs tabular-nums text-stone-600"
                >
                  {editForm.display_name.length}/100
                </p>
              </div>
              <div>
                <label
                  for="bio"
                  class="metadata-label mb-2 block text-stone-500">Bio</label
                >
                <textarea
                  id="bio"
                  bind:value={editForm.bio}
                  maxlength="2000"
                  rows="6"
                  class="w-full resize-y rounded-md border border-stone-800 bg-stone-950 px-3 py-2.5 text-sm leading-relaxed text-stone-100 placeholder-stone-600 transition-colors focus:border-stone-600 focus:outline-none"
                  placeholder="A few words about yourself"
                ></textarea>
                <p
                  class="mt-1.5 text-right text-xs tabular-nums text-stone-600"
                >
                  {editForm.bio.length}/2000
                </p>
              </div>
              {#if saveError}
                <div
                  role="alert"
                  class="rounded-md border border-down/30 bg-down/10 px-3 py-2.5 text-sm text-down"
                >
                  {saveError}
                </div>
              {/if}
              <div class="flex gap-2">
                <button
                  type="submit"
                  disabled={saving}
                  class="inline-flex flex-1 items-center justify-center gap-1.5 rounded-md bg-stone-100 px-3 py-2.5 text-sm font-medium leading-none text-stone-950 transition-colors hover:bg-stone-200 disabled:cursor-not-allowed disabled:opacity-50"
                >
                  {#if saving}
                    <Icon
                      icon="mdi:loading"
                      class="h-3.5 w-3.5 shrink-0 animate-spin"
                    />
                    <span class="optical-label">Saving</span>
                  {:else}
                    <OpticalIcon icon="mdi:check" size={14} box={14} />
                    <span class="optical-label">Save changes</span>
                  {/if}
                </button>
                <button
                  type="button"
                  disabled={saving}
                  on:click={cancelEditing}
                  class="rounded-md border border-stone-800 px-3 py-2.5 text-sm leading-none text-stone-400 transition-colors hover:border-stone-700 hover:text-stone-200 disabled:cursor-not-allowed disabled:opacity-50"
                  >Cancel</button
                >
              </div>
            </form>
          {:else}
            <div class="flex items-center gap-3 border-b border-stone-800 pb-4">
              <div
                class="flex h-11 w-11 shrink-0 items-center justify-center rounded-md border border-stone-800 bg-stone-950/60"
              >
                <Icon
                  icon="mdi:account-outline"
                  class="h-6 w-6 text-stone-500"
                />
              </div>
              <div class="min-w-0">
                <p class="truncate text-sm font-medium text-stone-200">
                  {displayName}
                </p>
                <p class="mt-1 truncate text-xs text-stone-600">
                  {profile.email || `@${profile.username}`}
                </p>
              </div>
            </div>
            <p
              class="py-4 text-sm leading-relaxed {profile.bio
                ? 'text-stone-400'
                : 'italic text-stone-600'}"
            >
              {profile.bio || "No bio added yet."}
            </p>
            <dl
              class="divide-y divide-stone-800 border-t border-stone-800 text-sm"
            >
              <div class="flex items-center justify-between gap-4 py-3">
                <dt class="metadata-label text-stone-600">Member since</dt>
                <dd
                  class="text-right tabular-nums text-stone-400"
                  title={instantTitle(profile.joined_at)}
                >
                  {joinedDate}
                </dd>
              </div>
              <div class="flex items-center justify-between gap-4 py-3">
                <dt class="metadata-label text-stone-600">Account</dt>
                <dd class="capitalize text-stone-400">{profile.role}</dd>
              </div>
            </dl>
          {/if}
        </Card>

        <Card title="Difficulty progress">
          {#if difficultyRows.length}
            <DifficultyBars rows={difficultyRows} />
          {:else}
            <p class="py-6 text-center text-sm text-stone-600">
              No challenge data yet.
            </p>
          {/if}
        </Card>

        <Card title="Activity totals">
          <div class="divide-y divide-stone-800">
            <div
              class="flex items-center justify-between gap-4 py-2.5 first:pt-0"
            >
              <span class="metadata-label text-stone-600">Attempts</span>
              <span class="tabular-nums text-stone-300"
                >{stats.total_attempts.toLocaleString()}</span
              >
            </div>
            <div class="flex items-center justify-between gap-4 py-2.5">
              <span class="metadata-label text-stone-600">Hints opened</span>
              <span class="tabular-nums text-stone-300"
                >{stats.hints_unlocked.toLocaleString()}</span
              >
            </div>
            <div
              class="flex items-center justify-between gap-4 py-2.5 last:pb-0"
            >
              <span class="metadata-label text-stone-600">Hint cost</span>
              <span class="tabular-nums text-stone-300"
                >{stats.points_spent_on_hints.toLocaleString()} pts</span
              >
            </div>
          </div>
        </Card>
      </section>

      <section class="min-w-0 space-y-6">
        {#if solves.length}
          <Card title="Solve points over time">
            <span slot="meta" class="metadata-label text-stone-500"
              >Solve points</span
            >
            <ProfileScoreChart points={scorePoints} height={240} />
          </Card>

          <div class="grid gap-6 md:grid-cols-2">
            <Card title="Points by category">
              <span slot="meta" class="metadata-label text-stone-500"
                >{categoryPoints.length} categories</span
              >
              <CategoryBars categories={categoryPoints} />
            </Card>

            <Card title="Challenge progress">
              <span slot="meta" class="text-xs tabular-nums text-stone-500"
                >{completedChallenges}/{challenges.length}</span
              >
              <div class="space-y-3">
                {#each categoryRows as row}
                  <div>
                    <div
                      class="mb-1.5 flex items-center gap-2 text-xs leading-none"
                    >
                      <span
                        class="h-2 w-2 shrink-0 rounded-full"
                        style="background: {row.color};"
                      ></span>
                      <span
                        class="optical-label truncate text-stone-300"
                        title={row.name}>{row.name}</span
                      >
                      <span
                        class="optical-label ml-auto tabular-nums text-stone-500"
                        >{row.solved}/{row.total}</span
                      >
                    </div>
                    <div
                      class="h-2 overflow-hidden rounded-full bg-stone-800/70"
                    >
                      <div
                        class="h-full rounded-full transition-[width] duration-300"
                        style="width: {row.total
                          ? Math.min(100, (row.solved / row.total) * 100)
                          : 0}%; background: {row.color}; opacity: 0.78;"
                      ></div>
                    </div>
                  </div>
                {/each}
              </div>
            </Card>
          </div>
        {:else}
          <Card title="Progress">
            <EmptyState icon="mdi:flag-outline" text="No flags captured yet.">
              <a
                href="/challenges"
                class="mt-4 inline-flex items-center gap-1.5 rounded-md border border-stone-700 px-3 py-2 text-sm leading-none text-stone-300 transition-colors hover:border-stone-600 hover:text-stone-100"
              >
                <OpticalIcon icon="mdi:flag-outline" size={14} box={14} />
                <span class="optical-label">Browse challenges</span>
              </a>
            </EmptyState>
          </Card>
        {/if}

        <Card title="Recent captures">
          <span slot="meta" class="metadata-label text-stone-500"
            >Local time</span
          >
          {#if recentSolves.length === 0}
            <p class="py-10 text-center text-sm text-stone-600">
              Nothing captured yet.
            </p>
          {:else}
            <div class="divide-y divide-stone-800">
              {#each recentSolves as solve}
                <a
                  href="/challenges/{solve.challenge_slug}"
                  class="group flex items-center gap-3 py-3 first:pt-0 last:pb-0"
                >
                  <span
                    class="flex h-7 w-7 shrink-0 items-center justify-center rounded border border-up/25 bg-up/10 text-up"
                  >
                    <OpticalIcon icon="mdi:check" size={13} box={14} />
                  </span>
                  <span class="min-w-0 flex-1">
                    <span
                      class="block truncate text-sm text-stone-300 transition-colors group-hover:text-stone-100"
                      >{solve.challenge_name}</span
                    >
                    <span
                      class="metadata-label mt-1 block truncate text-stone-600"
                      >{solve.flag_name}</span
                    >
                  </span>
                  <span class="shrink-0 text-right">
                    <span class="block text-sm font-medium tabular-nums text-up"
                      >+{solve.points}</span
                    >
                    <span
                      class="mt-1 block text-xs tabular-nums text-stone-600"
                      title={instantTitle(solve.solved_at)}
                      >{formatLocalDateLong(solve.solved_at)} · {formatLocalTime(
                        solve.solved_at,
                      )}</span
                    >
                  </span>
                </a>
              {/each}
            </div>
            <p
              class="mt-4 border-t border-stone-800 pt-3 text-xs text-stone-600"
            >
              Times shown in {viewerTimeZoneLabel}.
            </p>
          {/if}
        </Card>
      </section>
    </div>
  {/if}
</div>
