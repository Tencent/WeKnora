// 设置卡片左侧 logo 查询表。
//
// 资源被分到两类：
//   color/ —— 厂商官方多色 SVG，<img> 直渲，保留品牌原色
//   mono/  —— 单色 SVG（多取自 simple-icons / 厂商 mark），用 mask-image 染成
//             卡片自身的品牌 color，从而沿用 .store-card--<id> 这类规则定义的
//             低饱和品牌色调。
//
// 调用方传入 (category, id) 拿到 { mode, url }；找不到时返回 undefined，
// 卡片会回落到原有的首字母 monogram。

const colorModules = import.meta.glob('@/assets/img/providers/color/*/*.svg', {
  eager: true,
  query: '?url',
  import: 'default',
}) as Record<string, string>;

const monoModules = import.meta.glob('@/assets/img/providers/mono/*/*.svg', {
  eager: true,
  query: '?url',
  import: 'default',
}) as Record<string, string>;

export type ProviderCategory = 'vectorstore' | 'storage' | 'websearch' | 'parser';

export type LogoMatch = {
  mode: 'color' | 'mono';
  url: string;
};

const buildLookup = (modules: Record<string, string>, segment: string) => {
  const map: Partial<Record<ProviderCategory, Record<string, string>>> = {};
  const re = new RegExp(`providers/${segment}/([^/]+)/([^/]+)\\.svg$`);
  for (const [path, url] of Object.entries(modules)) {
    const match = path.match(re);
    if (!match) continue;
    const [, category, id] = match;
    const bucket = (map[category as ProviderCategory] ||= {});
    bucket[id.toLowerCase()] = url;
  }
  return map;
};

const colorLookup = buildLookup(colorModules, 'color');
const monoLookup = buildLookup(monoModules, 'mono');

export function providerLogo(
  category: ProviderCategory,
  id: string | undefined | null,
): LogoMatch | undefined {
  if (!id) return undefined;
  const key = id.toLowerCase();
  const colorUrl = colorLookup[category]?.[key];
  if (colorUrl) return { mode: 'color', url: colorUrl };
  const monoUrl = monoLookup[category]?.[key];
  if (monoUrl) return { mode: 'mono', url: monoUrl };
  return undefined;
}
