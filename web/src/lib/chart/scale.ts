// Minimal linear scale + nice axis rounding. Zero dependency.

export type Scale = (v: number) => number;

export function linear(d0: number, d1: number, r0: number, r1: number): Scale {
	const dd = d1 - d0;
	if (dd === 0) return () => r0;
	return (v: number) => r0 + ((v - d0) / dd) * (r1 - r0);
}

export function niceMax(max: number): number {
	if (max <= 0) return 1;
	const base = Math.pow(10, Math.floor(Math.log10(max)));
	const f = max / base;
	const nf = f <= 1 ? 1 : f <= 2 ? 2 : f <= 5 ? 5 : 10;
	return nf * base;
}
