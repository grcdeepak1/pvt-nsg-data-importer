# nsg-data-importer

This project hosts the implementation for nsg-data-importer which is ingesting SNMP data comming in via kafka and pushing it to Victoria Metrics. These timeseries can be later used for further analysis or event correlation.

## Deployment
This project will be deployed with replica count of 2 for loadbalancing and redudancy.