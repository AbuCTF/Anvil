// Monotone-cubic interpolation to an SVG path string. Zero dependency.

export interface Pt {
	x: number;
	y: number;
}

export interface Series {
	label: string;
	color: string;
	points: Pt[];
}

const r = (n: number) => Math.round(n * 100) / 100;

// Step-after: the value holds until the next x, then jumps. Accurate for a
// cumulative score, which is flat between solves and steps up at each solve.
export function stepPath(points: Pt[]): string {
	const n = points.length;
	if (n === 0) return '';
	let d = `M ${r(points[0].x)} ${r(points[0].y)}`;
	for (let i = 1; i < n; i++) {
		d += ` L ${r(points[i].x)} ${r(points[i - 1].y)} L ${r(points[i].x)} ${r(points[i].y)}`;
	}
	return d;
}

// A smooth, overshoot-free cubic through the points (Fritsch-Carlson tangents).
export function monotonePath(points: Pt[]): string {
	const n = points.length;
	if (n === 0) return '';
	if (n === 1) return `M ${r(points[0].x)} ${r(points[0].y)}`;

	const dx: number[] = [];
	const m: number[] = [];
	for (let i = 0; i < n - 1; i++) {
		const hx = points[i + 1].x - points[i].x;
		dx.push(hx);
		m.push(hx === 0 ? 0 : (points[i + 1].y - points[i].y) / hx);
	}

	const t: number[] = new Array(n);
	t[0] = m[0];
	t[n - 1] = m[n - 2];
	for (let i = 1; i < n - 1; i++) {
		t[i] = m[i - 1] * m[i] <= 0 ? 0 : (m[i - 1] + m[i]) / 2;
	}
	for (let i = 0; i < n - 1; i++) {
		if (m[i] === 0) {
			t[i] = 0;
			t[i + 1] = 0;
			continue;
		}
		const a = t[i] / m[i];
		const b = t[i + 1] / m[i];
		const s = a * a + b * b;
		if (s > 9) {
			const tau = 3 / Math.sqrt(s);
			t[i] = tau * a * m[i];
			t[i + 1] = tau * b * m[i];
		}
	}

	let d = `M ${r(points[0].x)} ${r(points[0].y)}`;
	for (let i = 0; i < n - 1; i++) {
		const h = dx[i];
		const c1x = points[i].x + h / 3;
		const c1y = points[i].y + (t[i] * h) / 3;
		const c2x = points[i + 1].x - h / 3;
		const c2y = points[i + 1].y - (t[i + 1] * h) / 3;
		d += ` C ${r(c1x)} ${r(c1y)}, ${r(c2x)} ${r(c2y)}, ${r(points[i + 1].x)} ${r(points[i + 1].y)}`;
	}
	return d;
}
