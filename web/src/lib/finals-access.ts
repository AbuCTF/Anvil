// finals access. tt = team number, bb = box 01-16, s = service 1-6, h = hill 1-3
// public ingress (no vpn) is built but off; flip this once the gateway opens it
export const PUBLIC_ACCESS = false;
export const PUBLIC_HOST = '140.245.253.126';
export const sshPort = (tt: number) => (PUBLIC_ACCESS ? 22000 + tt : 22);
export const servicePort = (bb: number, s: number) => (PUBLIC_ACCESS ? 30000 + bb * 10 + s : 7000 + s);
export const hillPort = (tt: number, h: number) => (PUBLIC_ACCESS ? 20000 + tt * 10 + h : 7100 + h);

export const SERVICES = ['Sign Me In', 'Can We Settle This?', 'Update Pending', 'Book Me If You Can', 'Ship It', 'Terms May Apply'];
export const TCP_SERVICE = 3;
export const HILLS = ['Current Affairs', 'Majority Rules', "Tag You're It"];
export const BOX_COUNT = 16;
export const NOP_BOX = 16;

export const pad2 = (n: number) => String(n).padStart(2, '0');
export const sshCommand = (tt: number) =>
	PUBLIC_ACCESS ? `ssh -p ${sshPort(tt)} ubuntu@${PUBLIC_HOST}` : `ssh ubuntu@10.60.${tt}.2`;
export const serviceAddr = (bb: number, s: number) =>
	`${PUBLIC_ACCESS ? PUBLIC_HOST : `10.60.${bb}.2`}:${servicePort(bb, s)}`;
export const hillUrl = (tt: number, h: number) =>
	`http://${PUBLIC_ACCESS ? PUBLIC_HOST : `10.60.210.1${h}`}:${hillPort(tt, h)}`;

// vpn box is 10.60.TT.2
export const vpnBoxAddr = (tt: number) => `10.60.${tt}.2`;

// vpn hills live at 10.60.210.1H:710H
export const vpnHillAddr = (h: number) => `10.60.210.1${h}:710${h}`;
export function hillFromVpnUrl(url: string): number | null {
	const m = /10\.60\.210\.1(\d):710(\d)/.exec(url);
	return m && m[1] === m[2] ? Number(m[1]) : null;
}
