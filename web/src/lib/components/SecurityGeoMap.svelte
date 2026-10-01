<script lang="ts">
	export let events: any[] = [];

	type Point = {
		x: number;
		y: number;
		count: number;
		label: string;
		simulated: boolean;
	};

	$: points = Array.from(
		events
			.filter((event) => Number.isFinite(event.latitude) && Number.isFinite(event.longitude))
			.reduce((groups: Map<string, Point>, event) => {
				const latitude = Number(event.latitude);
				const longitude = Number(event.longitude);
				const key = `${latitude.toFixed(3)}:${longitude.toFixed(3)}`;
				const current = groups.get(key);
				if (current) {
					current.count += 1;
					current.simulated = current.simulated && event.evidence_source === 'demo_simulated';
				} else {
					groups.set(key, {
						x: ((longitude + 180) / 360) * 800,
						y: ((90 - latitude) / 180) * 360,
						count: 1,
						label: [event.city, event.region, event.country_code].filter(Boolean).join(', ') || 'Approximate location',
						simulated: event.evidence_source === 'demo_simulated'
					});
				}
				return groups;
			}, new Map<string, Point>())
			.values()
	);
</script>

<div class="relative overflow-hidden rounded-lg border border-stone-800 bg-stone-950/70">
	<svg viewBox="0 0 800 360" class="block h-auto w-full" role="img" aria-label="Approximate activity locations">
		<defs>
			<linearGradient id="security-map-sea" x1="0" y1="0" x2="0" y2="1">
				<stop offset="0" stop-color="#0c0a09" />
				<stop offset="1" stop-color="#12100e" />
			</linearGradient>
			<filter id="security-map-glow" x="-100%" y="-100%" width="300%" height="300%">
				<feGaussianBlur stdDeviation="5" />
			</filter>
		</defs>
		<rect width="800" height="360" fill="url(#security-map-sea)" />
		<g stroke="#292524" stroke-width="1" opacity="0.65">
			{#each [90, 180, 270, 360, 450, 540, 630, 720] as x}
				<line x1={x} y1="0" x2={x} y2="360" />
			{/each}
			{#each [60, 120, 180, 240, 300] as y}
				<line x1="0" y1={y} x2="800" y2={y} />
			{/each}
		</g>
		<g fill="#292524" stroke="#44403c" stroke-width="1.2">
			<path d="M54 91 L86 54 151 44 197 64 216 96 185 113 166 145 119 139 91 116 67 120Z" />
			<path d="M178 157 L211 170 228 210 216 252 193 308 171 278 161 220Z" />
			<path d="M346 68 L396 47 457 57 481 83 532 82 575 105 620 103 679 130 661 158 614 154 583 175 540 157 504 169 467 141 428 139 398 111 364 109Z" />
			<path d="M385 141 L438 149 469 188 454 244 420 290 388 261 370 211Z" />
			<path d="M632 228 L676 211 721 229 738 263 704 286 659 275Z" />
			<path d="M295 70 L315 57 334 71 324 89 301 89Z" />
			<path d="M700 116 L716 107 726 121 715 139Z" />
		</g>
		{#each points as point}
			<g>
				<circle cx={point.x} cy={point.y} r={10 + Math.min(point.count, 8)} fill={point.simulated ? '#f59e0b' : '#22c55e'} opacity="0.25" filter="url(#security-map-glow)" />
				<circle cx={point.x} cy={point.y} r={5 + Math.min(point.count, 5)} fill={point.simulated ? '#f59e0b' : '#22c55e'} opacity="0.92">
					<title>{point.label}: {point.count} event{point.count === 1 ? '' : 's'}{point.simulated ? ' (demo data)' : ''}</title>
				</circle>
				{#if point.count > 1}
					<text x={point.x} y={point.y + 3} text-anchor="middle" font-size="8" font-weight="700" fill="#0c0a09">{point.count}</text>
				{/if}
			</g>
		{/each}
	</svg>
	{#if points.length === 0}
		<div class="absolute inset-0 flex items-center justify-center bg-stone-950/45 text-sm text-stone-500">
			No location evidence available
		</div>
	{/if}
	<div class="absolute bottom-3 left-3 flex items-center gap-3 rounded-md border border-stone-800 bg-stone-950/85 px-3 py-2 text-[10px] uppercase tracking-wider text-stone-500 backdrop-blur">
		<span class="flex items-center gap-1.5"><span class="h-2 w-2 rounded-full bg-up"></span>Live</span>
		<span class="flex items-center gap-1.5"><span class="h-2 w-2 rounded-full bg-amber-500"></span>Demo</span>
	</div>
</div>
