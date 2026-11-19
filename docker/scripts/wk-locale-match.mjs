const UTC_ZONES = new Set(["UTC", "Etc/UTC", "Etc/GMT", "GMT", "Etc/Universal", "Etc/Zulu"]);
const TIME_ZONE_ID_RE = /^[A-Za-z][A-Za-z0-9_+-]*(?:\/[A-Za-z0-9_+-]+)*$/;

export const PRINCIPAL_TIME_ZONES = {
  AR: "America/Argentina/Buenos_Aires",
  AU: "Australia/Sydney",
  BR: "America/Sao_Paulo",
  CA: "America/Toronto",
  CD: "Africa/Kinshasa",
  CL: "America/Santiago",
  CN: "Asia/Shanghai",
  CY: "Asia/Nicosia",
  DE: "Europe/Berlin",
  EC: "America/Guayaquil",
  ES: "Europe/Madrid",
  ID: "Asia/Jakarta",
  KZ: "Asia/Almaty",
  MN: "Asia/Ulaanbaatar",
  MX: "America/Mexico_City",
  MY: "Asia/Kuala_Lumpur",
  NZ: "Pacific/Auckland",
  PG: "Pacific/Port_Moresby",
  PT: "Europe/Lisbon",
  RU: "Europe/Moscow",
  UA: "Europe/Kyiv",
  US: "America/New_York",
  UZ: "Asia/Tashkent",
};

const US = "America/";
export const REGION_TIME_ZONES = {
  US: {
    AK: `${US}Anchorage`,
    AL: `${US}Chicago`,
    AR: `${US}Chicago`,
    AZ: `${US}Phoenix`,
    CA: `${US}Los_Angeles`,
    CO: `${US}Denver`,
    CT: `${US}New_York`,
    DC: `${US}New_York`,
    DE: `${US}New_York`,
    FL: `${US}New_York`,
    GA: `${US}New_York`,
    HI: "Pacific/Honolulu",
    IA: `${US}Chicago`,
    ID: `${US}Boise`,
    IL: `${US}Chicago`,
    IN: `${US}Indiana/Indianapolis`,
    KS: `${US}Chicago`,
    KY: `${US}New_York`,
    LA: `${US}Chicago`,
    MA: `${US}New_York`,
    MD: `${US}New_York`,
    ME: `${US}New_York`,
    MI: `${US}Detroit`,
    MN: `${US}Chicago`,
    MO: `${US}Chicago`,
    MS: `${US}Chicago`,
    MT: `${US}Denver`,
    NC: `${US}New_York`,
    ND: `${US}Chicago`,
    NE: `${US}Chicago`,
    NH: `${US}New_York`,
    NJ: `${US}New_York`,
    NM: `${US}Denver`,
    NV: `${US}Los_Angeles`,
    NY: `${US}New_York`,
    OH: `${US}New_York`,
    OK: `${US}Chicago`,
    OR: `${US}Los_Angeles`,
    PA: `${US}New_York`,
    RI: `${US}New_York`,
    SC: `${US}New_York`,
    SD: `${US}Chicago`,
    TN: `${US}Chicago`,
    TX: `${US}Chicago`,
    UT: `${US}Denver`,
    VA: `${US}New_York`,
    VT: `${US}New_York`,
    WA: `${US}Los_Angeles`,
    WI: `${US}Chicago`,
    WV: `${US}New_York`,
    WY: `${US}Denver`,
  },
  CA: {
    AB: `${US}Edmonton`,
    BC: `${US}Vancouver`,
    MB: `${US}Winnipeg`,
    NB: `${US}Moncton`,
    NL: `${US}St_Johns`,
    NS: `${US}Halifax`,
    NT: `${US}Edmonton`,
    NU: `${US}Iqaluit`,
    ON: `${US}Toronto`,
    PE: `${US}Halifax`,
    QC: `${US}Toronto`,
    SK: `${US}Regina`,
    YT: `${US}Whitehorse`,
  },
  AU: {
    ACT: "Australia/Sydney",
    NSW: "Australia/Sydney",
    NT: "Australia/Darwin",
    QLD: "Australia/Brisbane",
    SA: "Australia/Adelaide",
    TAS: "Australia/Hobart",
    VIC: "Australia/Melbourne",
    WA: "Australia/Perth",
  },
  BR: {
    AC: `${US}Rio_Branco`,
    AM: `${US}Manaus`,
    BA: `${US}Bahia`,
    CE: `${US}Fortaleza`,
    DF: `${US}Sao_Paulo`,
    MG: `${US}Sao_Paulo`,
    MS: `${US}Campo_Grande`,
    MT: `${US}Cuiaba`,
    PA: `${US}Belem`,
    PE: `${US}Recife`,
    PR: `${US}Sao_Paulo`,
    RJ: `${US}Sao_Paulo`,
    RO: `${US}Porto_Velho`,
    RS: `${US}Sao_Paulo`,
    SC: `${US}Sao_Paulo`,
    SP: `${US}Sao_Paulo`,
  },
  MX: {
    BCN: `${US}Tijuana`,
    CHH: `${US}Chihuahua`,
    CMX: `${US}Mexico_City`,
    JAL: `${US}Mexico_City`,
    NLE: `${US}Monterrey`,
    ROO: `${US}Cancun`,
    SIN: `${US}Mazatlan`,
    SON: `${US}Hermosillo`,
    YUC: `${US}Merida`,
  },
};

export function parseZoneTab(text) {
  const zonesByCountry = new Map();
  for (const line of String(text ?? "").split("\n")) {
    if (line.startsWith("#") || line.trim() === "") continue;
    const [codes, , zone] = line.split("\t");
    if (codes == null || zone == null) continue;
    for (const code of codes.split(",")) {
      const list = zonesByCountry.get(code) ?? [];
      list.push(zone);
      zonesByCountry.set(code, list);
    }
  }
  return zonesByCountry;
}

export function isKnownTimeZone(timeZone) {
  if (typeof timeZone !== "string" || timeZone.length > 64 || !TIME_ZONE_ID_RE.test(timeZone)) {
    return false;
  }
  try {
    new Intl.DateTimeFormat("en-US", { timeZone });
  } catch (error) {
    if (error instanceof RangeError) return false;
    throw error;
  }
  return true;
}

export function isUtcTimeZone(timeZone) {
  return UTC_ZONES.has(timeZone);
}

function userZone(userTimeZone) {
  return isKnownTimeZone(userTimeZone) && !isUtcTimeZone(userTimeZone) ? userTimeZone : null;
}

export function resolveTimeZone({ exitGeo, userTimeZone, zonesByCountry }) {
  const user = userZone(userTimeZone);
  const country = exitGeo?.country;
  const exitZone = isKnownTimeZone(exitGeo?.timeZone) ? exitGeo.timeZone : null;
  if (exitZone != null) return { timeZone: exitZone, source: "exit_geo" };
  if (country == null)
    return { timeZone: user, source: user == null ? "none" : "user_no_exit_geo" };
  const countryZones = zonesByCountry?.get(country) ?? [];
  const regionZone = REGION_TIME_ZONES[country]?.[exitGeo?.region ?? ""];
  if (regionZone != null) return { timeZone: regionZone, source: "exit_region" };
  if (user != null && countryZones.includes(user))
    return { timeZone: user, source: "user_in_exit_country" };
  if (countryZones.length === 1) return { timeZone: countryZones[0], source: "exit_country" };
  const principal = PRINCIPAL_TIME_ZONES[country] ?? countryZones[0] ?? null;
  return { timeZone: principal, source: principal == null ? "none" : "exit_country_guess" };
}
