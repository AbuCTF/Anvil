// Zero-dependency shareable rank card. Draws to an offscreen canvas in the stoic
// theme (JetBrains Mono) and triggers a PNG download. See DESIGN.md.

export interface RankCardData {
	rank: number;
	username: string;
	score: number;
	solves: number;
	delta: number;
	spark: number[];
	color: string;
}

const mono = (weight: number, size: number) => `${weight} ${size}px "JetBrains Mono Variable", "JetBrains Mono", ui-monospace, monospace`;

function drawSpark(ctx: CanvasRenderingContext2D, data: number[], color: string, x: number, y: number, w: number, h: number) {
	if (data.length < 2) return;
	const min = Math.min(...data);
	const max = Math.max(...data);
	const sx = (i: number) => x + (i / (data.length - 1)) * w;
	const sy = (v: number) => y + h - ((v - min) / (max === min ? 1 : max - min)) * h;

	// area
	ctx.beginPath();
	ctx.moveTo(sx(0), sy(data[0]));
	for (let i = 1; i < data.length; i++) {
		ctx.lineTo(sx(i), sy(data[i - 1]));
		ctx.lineTo(sx(i), sy(data[i]));
	}
	ctx.lineTo(sx(data.length - 1), y + h);
	ctx.lineTo(sx(0), y + h);
	ctx.closePath();
	ctx.fillStyle = color + '22';
	ctx.fill();

	// line (step)
	ctx.beginPath();
	ctx.moveTo(sx(0), sy(data[0]));
	for (let i = 1; i < data.length; i++) {
		ctx.lineTo(sx(i), sy(data[i - 1]));
		ctx.lineTo(sx(i), sy(data[i]));
	}
	ctx.strokeStyle = color;
	ctx.lineWidth = 1.5;
	ctx.lineJoin = 'round';
	ctx.stroke();
}

export async function downloadRankCard(d: RankCardData) {
	const W = 760;
	const H = 400;
	const scale = 2;
	const canvas = document.createElement('canvas');
	canvas.width = W * scale;
	canvas.height = H * scale;
	const ctx = canvas.getContext('2d');
	if (!ctx) return;
	ctx.scale(scale, scale);

	try {
		await Promise.all([document.fonts.load(mono(700, 92)), document.fonts.load(mono(600, 38)), document.fonts.load(mono(500, 14))]);
		await document.fonts.ready;
	} catch {
		/* fall back to whatever is available */
	}

	// ground + hairline border + team accent bar
	ctx.fillStyle = '#0c0a09';
	ctx.fillRect(0, 0, W, H);
	ctx.strokeStyle = '#292524';
	ctx.lineWidth = 1;
	ctx.strokeRect(0.5, 0.5, W - 1, H - 1);
	ctx.fillStyle = d.color;
	ctx.fillRect(0, 0, 4, H);

	ctx.textBaseline = 'alphabetic';

	// header
	ctx.fillStyle = '#78716c';
	ctx.font = mono(500, 14);
	ctx.fillText('Anvil  ·  Scoreboard', 40, 50);

	// rank + delta
	ctx.fillStyle = '#f59e0b';
	ctx.font = mono(700, 88);
	const rankText = '#' + d.rank;
	ctx.fillText(rankText, 38, 150);
	if (d.delta !== 0) {
		const rw = ctx.measureText(rankText).width;
		ctx.font = mono(600, 24);
		ctx.fillStyle = d.delta > 0 ? '#6fae7f' : '#d3776f';
		ctx.fillText((d.delta > 0 ? '▲' : '▼') + Math.abs(d.delta), 54 + rw, 150);
	}

	// name
	ctx.fillStyle = '#f5f5f4';
	ctx.font = mono(600, 36);
	ctx.fillText(d.username.length > 22 ? d.username.slice(0, 21) + '…' : d.username, 40, 208);

	// stats
	const stat = (x: number, label: string, value: string, accent = false) => {
		ctx.fillStyle = '#78716c';
		ctx.font = mono(500, 13);
		ctx.fillText(label, x, 258);
		ctx.fillStyle = accent ? '#f59e0b' : '#e7e5e4';
		ctx.font = mono(600, 28);
		ctx.fillText(value, x, 292);
	};
	stat(40, 'Points', d.score.toLocaleString(), true);
	stat(250, 'Solves', String(d.solves));

	// sparkline
	drawSpark(ctx, d.spark, d.color, 40, 326, W - 80, 44);

	await new Promise<void>((resolve) => {
		canvas.toBlob((blob) => {
			if (blob) {
				const url = URL.createObjectURL(blob);
				const a = document.createElement('a');
				a.href = url;
				a.download = `anvil-${d.username}-rank.png`;
				document.body.appendChild(a);
				a.click();
				a.remove();
				URL.revokeObjectURL(url);
			}
			resolve();
		}, 'image/png');
	});
}
