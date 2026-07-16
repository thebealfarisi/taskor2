import type { Metadata } from "next";
import { ChangelogPageClient } from "@/features/landing/components/changelog-page-client";

export const metadata: Metadata = {
  title: "Changelog",
  description:
    "See what's new in Task-Or — latest features, improvements, and fixes.",
  openGraph: {
    title: "Changelog | Task-Or",
    description: "Latest updates and releases from Task-Or.",
    url: "/changelog",
  },
  alternates: {
    canonical: "/changelog",
  },
};

export default function ChangelogPage() {
  return <ChangelogPageClient />;
}
