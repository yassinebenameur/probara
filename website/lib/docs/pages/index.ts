import type { DocPage } from "../types";
import { ADMINISTRATION_PAGE } from "./administration";
import { AGENTS_PAGE } from "./agents";
import { ALERTING_PAGE } from "./alerting";
import { API_PAGE } from "./api";
import { ARCHITECTURE_PAGE } from "./architecture";
import { DEPENDENCIES_PAGE } from "./dependencies";
import { GETTING_STARTED_PAGE } from "./getting-started";
import { LOCATIONS_PAGE } from "./locations";
import { MONITORS_PAGE } from "./monitors";
import { STATUS_PAGES_PAGE } from "./status-pages";

export {
  ADMINISTRATION_PAGE,
  AGENTS_PAGE,
  ALERTING_PAGE,
  API_PAGE,
  ARCHITECTURE_PAGE,
  DEPENDENCIES_PAGE,
  GETTING_STARTED_PAGE,
  LOCATIONS_PAGE,
  MONITORS_PAGE,
  STATUS_PAGES_PAGE,
};

export const PRODUCT_DOC_PAGES: DocPage[] = [
  GETTING_STARTED_PAGE,
  ARCHITECTURE_PAGE,
  MONITORS_PAGE,
  LOCATIONS_PAGE,
  ALERTING_PAGE,
  STATUS_PAGES_PAGE,
  DEPENDENCIES_PAGE,
  AGENTS_PAGE,
  ADMINISTRATION_PAGE,
  API_PAGE,
];
