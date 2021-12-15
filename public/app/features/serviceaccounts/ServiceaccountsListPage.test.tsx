import React from 'react';
import { shallow } from 'enzyme';
import { Props, ServiceaccountsListPage } from './ServiceaccountsListPage';
import { OrgServiceaccount } from 'app/types';
// import { getMockUser } from './__mocks__/userMocks';
import { NavModel } from '@grafana/data';

jest.mock('../../core/app_events', () => ({
  emit: jest.fn(),
}));

const setup = (propOverrides?: object) => {
  const props: Props = {
    navModel: {
      main: {
        text: 'Configuration',
      },
      node: {
        text: 'Users',
      },
    } as NavModel,
    serviceaccounts: [] as OrgServiceaccount[],
    searchQuery: '',
    searchPage: 1,
    loadserviceaccounts: jest.fn(),
    updateserviceaccount: jest.fn(),
    removeserviceaccount: jest.fn(),
    hasFetched: false,
  };

  Object.assign(props, propOverrides);

  const wrapper = shallow(<ServiceaccountsListPage {...props} />);
  const instance = wrapper.instance() as ServiceaccountsListPage;

  return {
    wrapper,
    instance,
  };
};

describe('Render', () => {
  it('should render component', () => {
    const { wrapper } = setup();

    expect(wrapper).toMatchSnapshot();
  });

  it('should render List page', () => {
    const { wrapper } = setup({
      hasFetched: true,
    });

    expect(wrapper).toMatchSnapshot();
  });
});
