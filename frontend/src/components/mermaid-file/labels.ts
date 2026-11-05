// The isolated origin has no application storage. Only toolbar translations cross the port.
export const labels: Record<string, string> = Object.create(null)
export default { global: { t: (key: string) => labels[key] ?? key } }
