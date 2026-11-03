alter table involvements rename constraint involvements_record_type_check to involvements_record_kind_check;
alter table involvements rename column record_type to record_kind;
alter table record_ids rename constraint record_ids_record_type_check to record_ids_kind_check;
alter table record_ids rename column record_type to kind;
