// Fixture: cert-manager addon — uses defaultProps shape.
import { HelmAddOn, HelmAddOnProps } from '../helm-addon';

const defaultProps: HelmAddOnProps = {
    name: 'blueprints-cert-manager-addon',
    namespace: 'cert-manager',
    chart: 'cert-manager',
    version: 'v1.19.4',
    release: 'cert-manager',
    repository: 'https://charts.jetstack.io',
    values: {},
};

export class CertManagerAddOn extends HelmAddOn {
    constructor(props?: any) {
        super({ ...defaultProps, ...props });
    }
}
