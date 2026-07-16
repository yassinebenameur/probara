import type { Config } from "tailwindcss";

const config: Config = {
  content: [
    "./pages/**/*.{js,ts,jsx,tsx,mdx}",
    "./components/**/*.{js,ts,jsx,tsx,mdx}",
    "./app/**/*.{js,ts,jsx,tsx,mdx}",
  ],
  theme: {
    extend: {
      colors: {
        primary: "#4f46e5",
        success: "#22c55e",
        error: "#ef4444",
        danger: "#ef4444",
        warning: "#facc15",
        muted: "#9ca3af",
      },
      borderRadius: {
        lg: "18px",
        xl: "22px",
        full: "999px",
      },
      boxShadow: {
        soft: "0 18px 45px rgba(0, 0, 0, 0.5)",
        subtle: "0 10px 30px rgba(0, 0, 0, 0.4)",
      },
    },
  },
  plugins: [],
};
export default config;

