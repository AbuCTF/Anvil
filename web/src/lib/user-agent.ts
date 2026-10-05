export type ClientSummary = {
	browser: string;
	os: string;
	device: string;
	label: string;
};

function version(value: string | undefined) {
	if (!value) return '';
	return value.replaceAll('_', '.').split('.').slice(0, 2).join('.');
}

function match(value: string, expression: RegExp) {
	return value.match(expression)?.[1];
}

export function describeUserAgent(value: string | null | undefined): ClientSummary {
	const source = value?.trim() ?? '';
	if (!source) return { browser: 'Unknown browser', os: 'Unknown OS', device: 'Unknown device', label: 'Client not recorded' };

	let browser = 'Unknown browser';
	const edge = match(source, /(?:Edg|EdgA|EdgiOS)\/([\d.]+)/);
	const opera = match(source, /(?:OPR|Opera)\/([\d.]+)/);
	const chrome = match(source, /(?:Chrome|CriOS)\/([\d.]+)/);
	const firefox = match(source, /(?:Firefox|FxiOS)\/([\d.]+)/);
	const safari = /Safari\//.test(source) ? match(source, /Version\/([\d.]+)/) : undefined;
	if (edge) browser = `Edge ${version(edge)}`;
	else if (opera) browser = `Opera ${version(opera)}`;
	else if (chrome) browser = `Chrome ${version(chrome)}`;
	else if (firefox) browser = `Firefox ${version(firefox)}`;
	else if (safari) browser = `Safari ${version(safari)}`;
	else if (/curl\//i.test(source)) browser = `curl ${version(match(source, /curl\/([\d.]+)/i))}`;
	else if (/python-requests\//i.test(source)) browser = `Python requests ${version(match(source, /python-requests\/([\d.]+)/i))}`;

	let os = 'Unknown OS';
	const windows = match(source, /Windows NT ([\d.]+)/);
	const android = match(source, /Android ([\d.]+)/);
	const ios = match(source, /(?:CPU (?:iPhone )?OS|iPhone OS) ([\d_]+)/);
	const macos = match(source, /Mac OS X ([\d_]+)/);
	if (windows === '10.0') os = 'Windows 10/11';
	else if (windows) os = `Windows ${version(windows)}`;
	else if (android) os = `Android ${version(android)}`;
	else if (ios) os = `iOS ${version(ios)}`;
	else if (macos) os = `macOS ${version(macos)}`;
	else if (/CrOS/.test(source)) os = 'ChromeOS';
	else if (/Linux/.test(source)) os = 'Linux';

	let device = 'Desktop';
	if (/iPad/i.test(source)) device = 'iPad';
	else if (/iPhone/i.test(source)) device = 'iPhone';
	else if (/Android/i.test(source) && /Mobile/i.test(source)) device = 'Android phone';
	else if (/Android/i.test(source)) device = 'Android tablet';
	else if (/Mobile/i.test(source)) device = 'Mobile';
	else if (/curl|python-requests/i.test(source)) device = 'Command line';

	return { browser, os, device, label: `${browser} · ${os} · ${device}` };
}
