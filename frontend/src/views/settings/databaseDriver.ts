export function formatDatabaseDriver(driver?: string): string {
  switch (driver?.trim().toLowerCase()) {
    case 'mysql':
      return 'MySQL'
    case 'postgres':
      return 'PostgreSQL'
    case 'sqlite':
      return 'SQLite'
    default:
      return driver?.trim() || ''
  }
}
