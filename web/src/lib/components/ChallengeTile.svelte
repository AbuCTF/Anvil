<script lang="ts">
	import Icon from '@iconify/svelte';
	import { auth } from '$stores/auth';
	import OpticalIcon from '$lib/components/OpticalIcon.svelte';
	import { difficultyClass, resourceClass, resourceIcon, resourceLabel } from '$lib/rank';

	export let challenge: {
		slug: string;
		name: string;
		difficulty: string;
		category?: string;
		resource_type?: string;
		has_instance?: boolean;
		has_attachments?: boolean;
		base_points?: number;
		points?: number;
		value?: number;
		total_flags: number;
		total_solves: number;
		user_solves: number;
		is_solved: boolean;
		author_name?: string;
		sub_description?: string;
		arena_mode?: string;
	};

	// resource_type is always 'docker' in data; the real signals are has_instance and
	// has_attachments. no instance and no files = an external/off-platform challenge.
	$: displayResource =
		challenge.resource_type === 'vm'
			? 'vm'
			: challenge.has_instance
				? 'docker'
				: challenge.has_attachments
					? 'static'
					: 'external';
	// economy boards carry the live value (band ceiling x crowd decay); else base points
	$: live = typeof challenge.value === 'number';
	$: points = live ? Math.round(challenge.value ?? 0) : (challenge.base_points ?? challenge.points ?? 0);
	$: multiFlag = challenge.total_flags > 1;
	$: progress = challenge.total_flags
		? Math.min(100, ((challenge.user_solves || 0) / challenge.total_flags) * 100)
		: 0;
</script>

<a
	href="/challenges/{challenge.slug}"
	class="tile group flex flex-col rounded-lg border p-4 transition-colors duration-150 {challenge.is_solved
		? 'challenge-tile-solved'
		: 'bg-stone-900/40 border-stone-800 hover:border-stone-700 hover:bg-stone-800/20'}"
>
	<div class="flex items-start justify-between gap-2">
		<h3 class="text-base font-semibold leading-snug {challenge.is_solved ? 'text-stone-200' : 'text-stone-100 group-hover:text-stone-50'} transition-colors">
			{challenge.name}
		</h3>
		{#if challenge.is_solved}
			<Icon icon="mdi:check-circle" class="challenge-tile-solved-mark w-4 h-4 shrink-0 mt-0.5" />
		{/if}
	</div>

	{#if challenge.sub_description}
		<p class="mt-1.5 text-sm text-stone-500 leading-relaxed line-clamp-2">{challenge.sub_description}</p>
	{/if}

	<div class="mt-3 flex flex-wrap items-center gap-2 leading-none">
		<span class="inline-flex items-center rounded border px-2 py-0.5 text-[0.68rem] leading-none font-medium capitalize {difficultyClass(challenge.difficulty)}">
			<span class="badge-label">{challenge.difficulty}</span>
		</span>
		{#if challenge.arena_mode === 'shared'}
			<span class="inline-flex items-center gap-1 rounded border border-amber-500/30 bg-amber-500/10 px-2 py-0.5 text-[0.68rem] leading-none font-medium text-amber-500" title="King of the Hill - shared contested arena">
				<OpticalIcon icon="mdi:crown-outline" size={12} box={12} />
				<span class="badge-label">KotH</span>
			</span>
		{/if}
		{#if challenge.resource_type}
			<span class="inline-flex items-center gap-1 rounded border px-2 py-0.5 text-[0.68rem] leading-none font-medium {resourceClass(displayResource)}">
				<OpticalIcon icon={resourceIcon(displayResource)} size={12} box={12} />
				<span class="badge-label">{resourceLabel(displayResource)}</span>
			</span>
		{/if}
	</div>

	<!-- footer pinned to the card bottom so every tile in a row aligns its stats,
	     and a taller neighbour (e.g. a multi-flag progress bar) only adds breathing
	     room above rather than dead space below shorter tiles -->
	<div class="mt-auto pt-3 border-t border-stone-800/60">
		{#if $auth.isAuthenticated && multiFlag}
			<div class="mb-3">
				<div class="flex items-baseline justify-between text-xs mb-1.5">
					<span class="metadata-label text-stone-500">Progress</span>
					<span class="optical-label text-stone-400 tabular-nums">{challenge.user_solves || 0}/{challenge.total_flags}</span>
				</div>
				<div class="w-full bg-stone-800 rounded-full h-1 overflow-hidden">
					<div class="h-full bg-up rounded-full transition-all duration-500" style="width: {progress}%"></div>
				</div>
			</div>
		{/if}
		<div class="flex items-center justify-between text-xs leading-none">
			<div class="flex items-center gap-3">
				<span class="inline-flex items-center gap-1 text-amber-500 font-semibold tabular-nums" title={live ? 'Points now: drops as more teams solve' : 'Points'}>
					<OpticalIcon icon="mdi:star-outline" size={12} box={12} />
					<span class="optical-label">{points}</span>
				</span>
				<span class="inline-flex items-center gap-1 text-stone-500 tabular-nums" title="Flags">
					<OpticalIcon icon="mdi:flag-outline" size={12} box={12} />
					<span class="optical-label">{challenge.total_flags}</span>
				</span>
			</div>
			<span class="inline-flex items-center gap-1 text-stone-500 tabular-nums" title="Solves">
				<OpticalIcon icon="mdi:account-group" size={12} box={12} />
				<span class="optical-label">{challenge.total_solves}</span>
			</span>
		</div>
	</div>
</a>

<style>
	@media (prefers-reduced-motion: no-preference) {
		.tile {
			animation: tileIn 0.2s ease both;
		}
	}
	@keyframes tileIn {
		from {
			opacity: 0;
			transform: translateY(4px);
		}
		to {
			opacity: 1;
			transform: translateY(0);
		}
	}
</style>
