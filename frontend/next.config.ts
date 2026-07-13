import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // Only relevant for `npm run dev`: add your dev machine's LAN IP here if you
  // open the dev server from another device (e.g. ["192.168.1.10", "localhost"]).
  // Production builds (docker) ignore this.
  allowedDevOrigins: ["localhost"],
};

export default nextConfig;
