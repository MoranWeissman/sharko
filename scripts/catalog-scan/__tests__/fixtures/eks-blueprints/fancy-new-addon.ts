// Fixture: fancy-new-addon — exercises the brief's documented HELM_CHART_*
// constant style (older convention; covered as a fallback shape).
import { HelmAddOn } from '../helm-addon';

const HELM_CHART_NAME = 'fancy-new-addon';
const HELM_CHART_REPO = 'https://charts.fancy.example.invalid';
const HELM_CHART_VERSION = '0.4.2';
const HELM_CHART_NAMESPACE = 'fancy-system';

export class FancyNewAddonAddOn extends HelmAddOn {
    constructor() {
        super({
            chart: HELM_CHART_NAME,
            repository: HELM_CHART_REPO,
            version: HELM_CHART_VERSION,
            namespace: HELM_CHART_NAMESPACE,
        });
    }
}
