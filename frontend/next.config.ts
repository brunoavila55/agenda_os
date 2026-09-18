import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  output: "standalone",
  poweredByHeader: false,
  experimental: {
    optimizePackageImports: [],
    useTypeScriptCli: false
  }
};

export default nextConfig;
