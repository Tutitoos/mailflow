export type Category = "primary" | "promotions" | "social" | "updates" | "forums";

export type MailItem = {
  id: string;
  sender: string;
  senderEmail: string;
  subject: string;
  preview: string;
  body: string;
  date: string;
  category: Category;
  unread: boolean;
  starred?: boolean;
  important?: boolean;
  attachment?: string;
  provider: "Google" | "Microsoft" | "iCloud";
};

export const messages: MailItem[] = [
  {
    id: "m1",
    sender: "Vercel",
    senderEmail: "ship@vercel.com",
    subject: "Deployment completed successfully",
    preview: "mailflow-web is live and all checks passed.",
    body: "Your deployment completed successfully. Build output and runtime checks are available in the project dashboard.",
    date: "16:42",
    category: "primary",
    unread: true,
    important: true,
    provider: "Google",
  },
  {
    id: "m2",
    sender: "GitHub",
    senderEmail: "notifications@github.com",
    subject: "Security update for your repository",
    preview: "Dependabot opened an update for a direct dependency.",
    body: "A dependency used by Mailflow has a new security update. Review the pull request before merging it.",
    date: "15:08",
    category: "updates",
    unread: true,
    starred: true,
    provider: "Microsoft",
  },
  {
    id: "m3",
    sender: "Linear",
    senderEmail: "updates@linear.app",
    subject: "Your week in review",
    preview: "12 issues completed, 4 projects moved forward.",
    body: "Here is your weekly workspace summary. You completed twelve issues and moved four projects forward.",
    date: "13:27",
    category: "updates",
    unread: false,
    provider: "Google",
  },
  {
    id: "m4",
    sender: "Cloudflare",
    senderEmail: "no-reply@cloudflare.com",
    subject: "Weekly security summary",
    preview: "Traefik handled 18,420 requests this week.",
    body: "Your weekly security report is ready. No critical events were detected for the configured domain.",
    date: "12:04",
    category: "primary",
    unread: false,
    attachment: "security-summary.pdf",
    provider: "iCloud",
  },
  {
    id: "m5",
    sender: "Figma",
    senderEmail: "notifications@figma.com",
    subject: "New comments in Mailflow UI",
    preview: "Three comments were added to the inbox exploration.",
    body: "There are three new comments on your Mailflow interface exploration. Open the file to review them.",
    date: "11:36",
    category: "social",
    unread: true,
    provider: "Google",
  },
  {
    id: "m6",
    sender: "Apple Developer",
    senderEmail: "developer@insideapple.apple.com",
    subject: "Your notarization request was accepted",
    preview: "The macOS build is ready for distribution.",
    body: "The software submitted for notarization has been accepted and can now be distributed.",
    date: "10:18",
    category: "primary",
    unread: false,
    important: true,
    provider: "iCloud",
  },
  {
    id: "m7",
    sender: "Raycast",
    senderEmail: "hello@raycast.com",
    subject: "A calmer way to work",
    preview: "Discover improvements to window management and search.",
    body: "This month we are introducing a faster search experience and improvements to window management.",
    date: "09:45",
    category: "promotions",
    unread: false,
    provider: "Google",
  },
  {
    id: "m8",
    sender: "Postmark",
    senderEmail: "support@postmarkapp.com",
    subject: "DMARC report available",
    preview: "Your latest deliverability report is ready.",
    body: "The latest DMARC and deliverability report for your sender domain is now available.",
    date: "Yesterday",
    category: "updates",
    unread: false,
    attachment: "dmarc-report.csv",
    provider: "Microsoft",
  },
  {
    id: "m9",
    sender: "Notion",
    senderEmail: "team@makenotion.com",
    subject: "Guillem mentioned you",
    preview: "Take a look at the updated architecture notes.",
    body: "Guillem mentioned you in Architecture notes: the provider remains the source of truth.",
    date: "Yesterday",
    category: "social",
    unread: true,
    provider: "Google",
  },
  {
    id: "m10",
    sender: "Docker",
    senderEmail: "newsletter@docker.com",
    subject: "Compose patterns for smaller teams",
    preview: "Simple production deployments without an orchestration platform.",
    body: "Learn how small teams use Docker Compose for reliable, understandable production deployments.",
    date: "Sep 5",
    category: "forums",
    unread: false,
    provider: "Microsoft",
  },
];

export const categories: { id: Category; labelKey: string }[] = [
  { id: "primary", labelKey: "category.primary" },
  { id: "promotions", labelKey: "category.promotions" },
  { id: "social", labelKey: "category.social" },
  { id: "updates", labelKey: "category.updates" },
  { id: "forums", labelKey: "category.forums" },
];
