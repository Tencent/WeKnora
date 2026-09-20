import Image from "next/image";

/** Show the complete original artwork; the viewport only trims blank canvas. */
export function BrandLogo({ priority = false }: { priority?: boolean }) {
  return <span className="wk-logo"><Image src="/brand/weknora-original.png" alt="WeKnora" width={945} height={650} priority={priority} /></span>;
}
