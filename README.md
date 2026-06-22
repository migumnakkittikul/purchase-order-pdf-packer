# purchase-order-pdf-packer

A small tool for turning bulky SAP purchase-order PDFs into one compact PDF: an
order summary plus only the receiving labels that are actually needed.

Work in progress.

## Usage so far

Reads one or more PO PDFs and prints the parsed line items, together with the
number of receiving labels each line needs:

    go run . order.pdf
