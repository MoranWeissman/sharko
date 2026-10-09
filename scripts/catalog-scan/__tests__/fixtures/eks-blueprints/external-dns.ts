// Fixture: external-dns addon — uses defaultProps shape.
import { HelmAddOn, HelmAddOnUserProps } from '../helm-addon';

const defaultProps = {
    name: 'external-dns',
    chart: 'external-dns',
    namespace: 'external-dns',
    repository: 'https://kubernetes-sigs.github.io/external-dns/',
    release: 'blueprints-addon-external-dns',
    version: '1.20.0',
    values: {},
};

export class ExternalDnsAddOn extends HelmAddOn {
    constructor(props?: HelmAddOnUserProps) {
        super({ ...defaultProps, ...props });
    }
}
